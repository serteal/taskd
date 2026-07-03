package taskclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

// fakeSvc is a scripted ItemService: Watch runs one script entry per call
// (falling through to block-until-cancel), QueryItems serves fixed pages
// keyed by page token. It records since-cursors, requests, and the
// x-task-client metadata it saw.
type fakeSvc struct {
	taskcorev1.UnimplementedItemServiceServer

	mu          sync.Mutex
	watchScript []func(stream taskcorev1.ItemService_WatchServer) error
	pages       map[string]*taskcorev1.QueryItemsResponse

	watchSince []uint64
	watchMD    []string
	queryReqs  []*taskcorev1.QueryItemsRequest
	queryMD    []string
}

func mdClient(ctx context.Context) string {
	md, _ := metadata.FromIncomingContext(ctx)
	if v := md.Get(clientHeader); len(v) > 0 {
		return v[0]
	}
	return ""
}

func (f *fakeSvc) Watch(req *taskcorev1.WatchRequest, stream taskcorev1.ItemService_WatchServer) error {
	f.mu.Lock()
	idx := len(f.watchSince)
	f.watchSince = append(f.watchSince, req.GetSinceCursor())
	f.watchMD = append(f.watchMD, mdClient(stream.Context()))
	var fn func(taskcorev1.ItemService_WatchServer) error
	if idx < len(f.watchScript) {
		fn = f.watchScript[idx]
	}
	f.mu.Unlock()
	if fn == nil {
		<-stream.Context().Done()
		return nil
	}
	return fn(stream)
}

func (f *fakeSvc) QueryItems(ctx context.Context, req *taskcorev1.QueryItemsRequest) (*taskcorev1.QueryItemsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queryReqs = append(f.queryReqs, req)
	f.queryMD = append(f.queryMD, mdClient(ctx))
	resp, ok := f.pages[req.GetPageToken()]
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "unscripted page token %q", req.GetPageToken())
	}
	return resp, nil
}

func (f *fakeSvc) sinces() []uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uint64(nil), f.watchSince...)
}

func sendEvent(stream taskcorev1.ItemService_WatchServer, cursor uint64) error {
	return stream.Send(&taskcorev1.WatchResponse{Msg: &taskcorev1.WatchResponse_Event{
		Event: &taskcorev1.Event{
			Cursor: cursor,
			Type:   taskcorev1.ChangeType_CHANGE_TYPE_UPDATED,
			Item:   &taskcorev1.Item{Id: fmt.Sprintf("item-%d", cursor)},
		},
	}})
}

func sendCheckpoint(stream taskcorev1.ItemService_WatchServer, cursor uint64) error {
	return stream.Send(&taskcorev1.WatchResponse{Msg: &taskcorev1.WatchResponse_Checkpoint{
		Checkpoint: &taskcorev1.Checkpoint{Cursor: cursor},
	}})
}

func block(stream taskcorev1.ItemService_WatchServer) error {
	<-stream.Context().Done()
	return nil
}

// startFake serves svc over a real unix socket (short t.TempDir: macOS caps
// socket paths at 104 bytes) and dials it through the SDK's own Dial, so the
// interceptors are under test too.
func startFake(t *testing.T, svc taskcorev1.ItemServiceServer) *Client {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	g := grpc.NewServer()
	taskcorev1.RegisterItemServiceServer(g, svc)
	go g.Serve(ln)
	t.Cleanup(g.Stop)

	c, err := Dial(context.Background(), sock, "test-client")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// recHandler records the exact delivery sequence and cancels the watch after
// seeing stopAfter's cursor.
type recHandler struct {
	mu        sync.Mutex
	seq       []string
	resyncs   int
	stopAfter uint64
	cancel    context.CancelFunc
	eventErr  error // returned from HandleEvent when set
}

func (h *recHandler) HandleEvent(ev *taskcorev1.Event) error {
	h.mu.Lock()
	h.seq = append(h.seq, fmt.Sprintf("e:%d", ev.GetCursor()))
	h.mu.Unlock()
	if h.eventErr != nil {
		return h.eventErr
	}
	if ev.GetCursor() == h.stopAfter && h.cancel != nil {
		h.cancel()
	}
	return nil
}

func (h *recHandler) HandleResync(replay func(fn func(*taskcorev1.Item) error) error) (uint64, error) {
	var ids []string
	if err := replay(func(it *taskcorev1.Item) error {
		ids = append(ids, it.GetId())
		return nil
	}); err != nil {
		return 0, err
	}
	h.mu.Lock()
	h.resyncs++
	h.seq = append(h.seq, "resync:"+strings.Join(ids, ","))
	h.mu.Unlock()
	return 0, nil // resume from the replay's snapshot cursor
}

func (h *recHandler) HandleCheckpoint(cursor uint64) error {
	h.mu.Lock()
	h.seq = append(h.seq, fmt.Sprintf("cp:%d", cursor))
	h.mu.Unlock()
	return nil
}

func (h *recHandler) sequence() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.seq...)
}

func eq[T comparable](t *testing.T, name string, got, want []T) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
	}
}

// TestResync is the reason this SDK exists: events, then CURSOR_EXPIRED,
// then a snapshot replay, then post-resync events — with no gap or
// duplicate, and the second watch resuming from the snapshot cursor.
func TestResync(t *testing.T) {
	f := &fakeSvc{
		watchScript: []func(taskcorev1.ItemService_WatchServer) error{
			func(s taskcorev1.ItemService_WatchServer) error {
				if err := sendEvent(s, 6); err != nil {
					return err
				}
				if err := sendEvent(s, 7); err != nil {
					return err
				}
				return status.Errorf(codes.FailedPrecondition,
					"CURSOR_EXPIRED: cursor 7 outside retained log [40, 44]")
			},
			func(s taskcorev1.ItemService_WatchServer) error {
				if err := sendEvent(s, 43); err != nil {
					return err
				}
				if err := sendEvent(s, 44); err != nil {
					return err
				}
				return block(s)
			},
		},
		pages: map[string]*taskcorev1.QueryItemsResponse{
			"": {
				Items:  []*taskcorev1.Item{{Id: "A"}, {Id: "B"}},
				Cursor: 42,
			},
		},
	}
	c := startFake(t, f)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := &recHandler{stopAfter: 44, cancel: cancel}

	if err := c.WatchItems(ctx, `!completed`, 5, h); err != nil {
		t.Fatalf("WatchItems = %v, want nil after cancel", err)
	}
	eq(t, "sequence", h.sequence(), []string{"e:6", "e:7", "resync:A,B", "e:43", "e:44"})
	if h.resyncs != 1 {
		t.Fatalf("resyncs = %d, want 1", h.resyncs)
	}
	// The resync must resume exactly at the snapshot cursor: no gap, no
	// duplicate.
	eq(t, "watch since-cursors", f.sinces(), []uint64{5, 42})

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.queryReqs) != 1 || f.queryReqs[0].GetFilter() != `!completed` {
		t.Fatalf("resync must QueryAll with the same filter, got %v", f.queryReqs)
	}
}

// TestLagged: ResourceExhausted means the log still covers us — re-watch
// immediately from the last delivered cursor (a checkpoint advanced it).
func TestLagged(t *testing.T) {
	f := &fakeSvc{
		watchScript: []func(taskcorev1.ItemService_WatchServer) error{
			func(s taskcorev1.ItemService_WatchServer) error {
				if err := sendEvent(s, 2); err != nil {
					return err
				}
				if err := sendCheckpoint(s, 3); err != nil {
					return err
				}
				return status.Error(codes.ResourceExhausted, "watch lagged behind the feed")
			},
			func(s taskcorev1.ItemService_WatchServer) error {
				if err := sendEvent(s, 4); err != nil {
					return err
				}
				return block(s)
			},
		},
	}
	c := startFake(t, f)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := &recHandler{stopAfter: 4, cancel: cancel}

	if err := c.WatchItems(ctx, "", 1, h); err != nil {
		t.Fatalf("WatchItems = %v, want nil", err)
	}
	eq(t, "sequence", h.sequence(), []string{"e:2", "cp:3", "e:4"})
	if h.resyncs != 0 {
		t.Fatalf("lag must not resync, got %d resyncs", h.resyncs)
	}
	eq(t, "watch since-cursors", f.sinces(), []uint64{1, 3})
}

// TestBadFilter: InvalidArgument is the caller's bug; return it, never retry.
func TestBadFilter(t *testing.T) {
	f := &fakeSvc{
		watchScript: []func(taskcorev1.ItemService_WatchServer) error{
			func(taskcorev1.ItemService_WatchServer) error {
				return status.Error(codes.InvalidArgument, "filter: undeclared reference")
			},
		},
	}
	c := startFake(t, f)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := c.WatchItems(ctx, "nope(", 0, &recHandler{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("WatchItems = %v, want InvalidArgument", err)
	}
	if got := f.sinces(); len(got) != 1 {
		t.Fatalf("bad filter must not be retried, got %d watch calls", len(got))
	}
}

// TestRetry: transient errors retry with backoff from the last cursor.
func TestRetry(t *testing.T) {
	f := &fakeSvc{
		watchScript: []func(taskcorev1.ItemService_WatchServer) error{
			func(taskcorev1.ItemService_WatchServer) error {
				return status.Error(codes.Unavailable, "transient")
			},
			func(s taskcorev1.ItemService_WatchServer) error {
				if err := sendEvent(s, 8); err != nil {
					return err
				}
				return block(s)
			},
		},
	}
	c := startFake(t, f)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := &recHandler{stopAfter: 8, cancel: cancel}

	start := time.Now()
	if err := c.WatchItems(ctx, "", 7, h); err != nil {
		t.Fatalf("WatchItems = %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Fatalf("retry happened after %v, want a ~250ms backoff first", elapsed)
	}
	eq(t, "sequence", h.sequence(), []string{"e:8"})
	eq(t, "watch since-cursors", f.sinces(), []uint64{7, 7})
}

// TestHandlerAbort: a handler error is the caller's decision to stop — it
// must come back out, not be retried like a transport error.
func TestHandlerAbort(t *testing.T) {
	f := &fakeSvc{
		watchScript: []func(taskcorev1.ItemService_WatchServer) error{
			func(s taskcorev1.ItemService_WatchServer) error {
				if err := sendEvent(s, 1); err != nil {
					return err
				}
				return block(s)
			},
		},
	}
	c := startFake(t, f)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	boom := errors.New("handler says stop")
	err := c.WatchItems(ctx, "", 0, &recHandler{eventErr: boom})
	if !errors.Is(err, boom) {
		t.Fatalf("WatchItems = %v, want the handler's error", err)
	}
}

func TestQueryAll(t *testing.T) {
	f := &fakeSvc{
		pages: map[string]*taskcorev1.QueryItemsResponse{
			"": {
				Items:         []*taskcorev1.Item{{Id: "1"}, {Id: "2"}, {Id: "3"}},
				NextPageToken: "p2",
				Cursor:        99,
			},
			"p2": {
				Items:  []*taskcorev1.Item{{Id: "4"}, {Id: "5"}},
				Cursor: 100, // later page; QueryAll must report the first
			},
		},
	}
	c := startFake(t, f)

	var ids []string
	cursor, err := c.QueryAll(context.Background(), `project == "x"`, "due", func(it *taskcorev1.Item) error {
		ids = append(ids, it.GetId())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if cursor != 99 {
		t.Fatalf("cursor = %d, want the first page's 99", cursor)
	}
	eq(t, "ids", ids, []string{"1", "2", "3", "4", "5"})

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.queryReqs) != 2 {
		t.Fatalf("got %d QueryItems calls, want 2", len(f.queryReqs))
	}
	for _, req := range f.queryReqs {
		if req.GetPageSize() != 200 || req.GetFilter() != `project == "x"` || req.GetOrderBy() != "due" {
			t.Fatalf("request %v: want page_size 200 and the caller's filter/order", req)
		}
	}
}

// TestHeader: both interceptors must attach x-task-client.
func TestHeader(t *testing.T) {
	f := &fakeSvc{
		watchScript: []func(taskcorev1.ItemService_WatchServer) error{
			func(taskcorev1.ItemService_WatchServer) error {
				return status.Error(codes.InvalidArgument, "stop here")
			},
		},
		pages: map[string]*taskcorev1.QueryItemsResponse{"": {}},
	}
	c := startFake(t, f)

	if _, err := c.QueryAll(context.Background(), "", "", func(*taskcorev1.Item) error { return nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.WatchItems(ctx, "", 0, &recHandler{})

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.queryMD) != 1 || f.queryMD[0] != "test-client" {
		t.Fatalf("unary metadata = %v, want [test-client]", f.queryMD)
	}
	if len(f.watchMD) != 1 || f.watchMD[0] != "test-client" {
		t.Fatalf("stream metadata = %v, want [test-client]", f.watchMD)
	}
}

func TestExpired(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{status.Error(codes.FailedPrecondition, "CURSOR_EXPIRED: cursor 3 outside retained log"), true},
		{status.Error(codes.FailedPrecondition, "something else"), false},
		{status.Error(codes.NotFound, "CURSOR_EXPIRED: wrong code"), false},
		{errors.New("CURSOR_EXPIRED plain error"), false},
		{nil, false},
	}
	for _, c := range cases {
		if got := IsCursorExpired(c.err); got != c.want {
			t.Errorf("IsCursorExpired(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}
