package intent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/clock"
	"todoapp/internal/feed"
	"todoapp/internal/query"
	"todoapp/internal/store"
)

// fakeDisp scripts the connector side of the router.
type fakeDisp struct {
	supports bool
	remote   *pluginv1.RemoteItem
	err      error
	calls    []*pluginv1.HandleIntentRequest
}

func (f *fakeDisp) Supports(string, string, pluginv1.Intent) bool { return f.supports }
func (f *fakeDisp) HandleIntent(_ context.Context, _ string, req *pluginv1.HandleIntentRequest) (*pluginv1.RemoteItem, error) {
	f.calls = append(f.calls, req)
	if f.err != nil {
		return nil, f.err
	}
	return f.remote, nil
}
func (f *fakeDisp) Resolve(context.Context, string, string) (*pluginv1.RemoteItem, error) {
	return nil, status.Error(codes.Unimplemented, "not used here")
}

type env struct {
	st   store.Store
	hub  *feed.Hub
	clk  *clock.Fake
	disp *fakeDisp
	r    *Router
	item string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC))
	qe, err := query.NewEngine(clk.Now)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(store.Options{
		Path: filepath.Join(t.TempDir(), "t.db"), Diff: feed.Diff, Extract: qe.Extract, Now: clk.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hub := feed.NewHub()
	disp := &fakeDisp{supports: true}
	ids := clock.NewIDGen(clk, 9)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	e := &env{st: st, hub: hub, clk: clk, disp: disp, r: NewRouter(st, hub, clk, ids, disp, log)}

	item := &taskcorev1.Item{
		Id: ids.NewID(), Kind: "todotxt.task",
		Mirror: &taskcorev1.Mirror{
			Title: "old title", State: "open",
			Link: &taskcorev1.ExternalLink{ConnectorInstance: "todo@t", ExternalId: "id-1"},
		},
		Todo:           &taskcorev1.Todo{Completed: true, CompletedReason: "done irl"},
		MirrorRevision: 1,
	}
	if _, err := st.CreateItem(context.Background(), item, &taskcorev1.Provenance{
		Source: taskcorev1.ProvenanceSource_PROVENANCE_SOURCE_SYNC, Ref: "todo@t",
	}); err != nil {
		t.Fatal(err)
	}
	e.item = item.GetId()
	return e
}

func TestInvokeValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	if _, err := e.r.Invoke(ctx, e.item, "explode", nil, 0); !errors.Is(err, ErrUnknownIntent) {
		t.Fatalf("unknown intent: %v", err)
	}
	e.disp.supports = false
	if _, err := e.r.Invoke(ctx, e.item, "rename", nil, 0); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported: %v", err)
	}
	e.disp.supports = true
	if _, err := e.r.Invoke(ctx, e.item, "rename", nil, 0); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("rename without params: %v", err)
	}

	// A native item has no remote to write to.
	native := &taskcorev1.Item{Id: "01NATIVE000000000000000000", Kind: "task",
		Todo: &taskcorev1.Todo{TitleOverride: "n"}, TodoRevision: 1}
	if _, err := e.st.CreateItem(ctx, native, &taskcorev1.Provenance{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.r.Invoke(ctx, native.GetId(), "rename", nil, 0); !errors.Is(err, ErrNotMirrored) {
		t.Fatalf("native item: %v", err)
	}
}

func TestConfirmFlow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.disp.remote = &pluginv1.RemoteItem{
		ExternalId: "id-1", Kind: "todotxt.task", Title: "new title", State: "open",
	}
	params, _ := structpb.NewStruct(map[string]any{"title": "new title"})
	rec, err := e.r.Invoke(ctx, e.item, "rename", params, 0)
	if err != nil {
		t.Fatal(err)
	}
	e.r.drain(ctx)

	got, err := e.st.GetIntent(ctx, rec.GetId())
	if err != nil || got.GetState() != taskcorev1.IntentState_INTENT_STATE_CONFIRMED {
		t.Fatalf("state = %v (%v), want CONFIRMED", got.GetState(), err)
	}
	item, _ := e.st.GetItem(ctx, e.item)
	if item.GetMirror().GetTitle() != "new title" {
		t.Fatalf("mirror not updated from confirmed state: %q", item.GetMirror().GetTitle())
	}
	if len(e.disp.calls) != 1 || e.disp.calls[0].GetIdempotencyKey() != rec.GetId() {
		t.Fatalf("dispatch calls = %v, want one with the record id as idempotency key", e.disp.calls)
	}
	// The user's todo layer is untouched, as always.
	if !item.GetTodo().GetCompleted() {
		t.Fatal("intent delivery damaged the todo layer")
	}
}

func TestDeriveParamsFromLocalState(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.disp.remote = &pluginv1.RemoteItem{ExternalId: "id-1", Kind: "todotxt.task", Title: "old title", State: "done"}

	// set_completed with no params pushes the item's CURRENT completion —
	// this is what rule write-back relies on.
	rec, err := e.r.Invoke(ctx, e.item, "set_completed", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	e.r.drain(ctx)
	if got := e.disp.calls[0].GetParams().GetFields(); got["completed"].GetBoolValue() != true ||
		got["reason"].GetStringValue() != "done irl" {
		t.Fatalf("derived params = %v", got)
	}
	if got, _ := e.st.GetIntent(ctx, rec.GetId()); got.GetState() != taskcorev1.IntentState_INTENT_STATE_CONFIRMED {
		t.Fatalf("state = %v", got.GetState())
	}
}

func TestTransientRetryThenFail(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.disp.err = status.Error(codes.Unavailable, "plugin down")
	params, _ := structpb.NewStruct(map[string]any{"title": "x"})
	rec, _ := e.r.Invoke(ctx, e.item, "rename", params, 0)

	e.r.drain(ctx)
	got, _ := e.st.GetIntent(ctx, rec.GetId())
	if got.GetState() != taskcorev1.IntentState_INTENT_STATE_QUEUED || got.GetAttempts() != 1 {
		t.Fatalf("after transient failure: state=%v attempts=%d", got.GetState(), got.GetAttempts())
	}
	if got.GetNextAttemptAt() == nil || !got.GetNextAttemptAt().AsTime().After(e.clk.Now()) {
		t.Fatal("transient failure must schedule a future retry")
	}
	// Not due yet: drain does nothing.
	e.r.drain(ctx)
	if got, _ = e.st.GetIntent(ctx, rec.GetId()); got.GetAttempts() != 1 {
		t.Fatal("retried before its backoff elapsed")
	}
	// Due after the backoff: attempts occur until maxAttempts, then FAILED.
	for i := 0; i < maxAttempts; i++ {
		e.clk.Advance(31 * time.Minute) // beyond the backoff cap
		e.r.drain(ctx)
	}
	got, _ = e.st.GetIntent(ctx, rec.GetId())
	if got.GetState() != taskcorev1.IntentState_INTENT_STATE_FAILED {
		t.Fatalf("state = %v after exhausting retries, want FAILED", got.GetState())
	}

	// Retry resets delivery; a healthy connector confirms it.
	e.disp.err = nil
	e.disp.remote = &pluginv1.RemoteItem{ExternalId: "id-1", Kind: "todotxt.task", Title: "x", State: "open"}
	if _, err := e.r.Retry(ctx, rec.GetId()); err != nil {
		t.Fatal(err)
	}
	e.r.drain(ctx)
	if got, _ = e.st.GetIntent(ctx, rec.GetId()); got.GetState() != taskcorev1.IntentState_INTENT_STATE_CONFIRMED {
		t.Fatalf("state after retry = %v, want CONFIRMED", got.GetState())
	}
}

func TestPermanentFailure(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.disp.err = status.Error(codes.NotFound, "line vanished")
	params, _ := structpb.NewStruct(map[string]any{"title": "x"})
	rec, _ := e.r.Invoke(ctx, e.item, "rename", params, 0)
	e.r.drain(ctx)

	got, _ := e.st.GetIntent(ctx, rec.GetId())
	if got.GetState() != taskcorev1.IntentState_INTENT_STATE_FAILED || got.GetAttempts() != 0 {
		t.Fatalf("permanent failure: state=%v attempts=%d, want FAILED without retries", got.GetState(), got.GetAttempts())
	}
	if got.GetLastError() == "" {
		t.Fatal("failure reason must be preserved verbatim")
	}

	// Discard is terminal; retrying a discarded intent is refused.
	if _, err := e.r.Discard(ctx, rec.GetId()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.r.Retry(ctx, rec.GetId()); !errors.Is(err, ErrNotRetryable) {
		t.Fatalf("retry after discard: %v", err)
	}
}

func TestBackoffShape(t *testing.T) {
	if backoff(1) != 30*time.Second || backoff(2) != time.Minute {
		t.Fatalf("backoff(1,2) = %v, %v", backoff(1), backoff(2))
	}
	if backoff(20) != 30*time.Minute {
		t.Fatalf("backoff cap = %v", backoff(20))
	}
}
