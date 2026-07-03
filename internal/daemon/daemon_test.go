package daemon

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

// TestDaemonEndToEnd drives one full item lifecycle over the real unix
// socket: ping, watch-from-now, create, prefix-addressed complete (with the
// server-stamped completed_at), query with pushdown+residual, mirror-write
// rejection, live watch delivery, and seeded views.
func TestDaemonEndToEnd(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- Run(ctx, Config{Dir: dir, IDSeed: 42}) }()
	t.Cleanup(func() {
		cancel()
		if err := <-errc; err != nil {
			t.Errorf("daemon exit: %v", err)
		}
	})

	sock := SocketPath(dir)
	waitFor(t, 5*time.Second, func() bool {
		_, err := os.Stat(sock)
		return err == nil
	})

	conn, err := grpc.NewClient("unix://"+sock, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	rpcCtx := metadata.AppendToOutgoingContext(ctx, "x-task-client", "daemon-test")

	// Socket must be owner-only.
	if fi, err := os.Stat(sock); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket permissions = %v, want 0600 (err %v)", fi.Mode().Perm(), err)
	}

	admin := taskcorev1.NewAdminServiceClient(conn)
	if _, err := admin.Ping(rpcCtx, &taskcorev1.PingRequest{ClientName: "daemon-test"}); err != nil {
		t.Fatalf("ping: %v", err)
	}

	items := taskcorev1.NewItemServiceClient(conn)

	w, err := items.Watch(rpcCtx, &taskcorev1.WatchRequest{})
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	anchor, err := w.Recv()
	if err != nil || anchor.GetCheckpoint() == nil {
		t.Fatalf("watch must anchor with a checkpoint first, got %v (err %v)", anchor, err)
	}
	events := make(chan *taskcorev1.Event, 16)
	go func() {
		for {
			r, err := w.Recv()
			if err != nil {
				close(events)
				return
			}
			if ev := r.GetEvent(); ev != nil {
				events <- ev
			}
		}
	}()

	created, err := items.CreateItem(rpcCtx, &taskcorev1.CreateItemRequest{Todo: &taskcorev1.Todo{
		TitleOverride: "smoke task", Labels: []string{"smoke"}, Project: "test",
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := created.GetItem().GetId()

	upd, err := items.UpdateItem(rpcCtx, &taskcorev1.UpdateItemRequest{
		Id:         id[:10], // unique prefix must resolve
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"todo.completed"}},
		Item:       &taskcorev1.Item{Todo: &taskcorev1.Todo{Completed: true}},
	})
	if err != nil {
		t.Fatalf("update via prefix: %v", err)
	}
	if upd.GetItem().GetTodo().GetCompletedAt() == nil {
		t.Fatal("completing must stamp completed_at server-side")
	}
	if got := upd.GetItem().GetTodoRevision(); got != 2 {
		t.Fatalf("todo_revision = %d, want 2", got)
	}

	q, err := items.QueryItems(rpcCtx, &taskcorev1.QueryItemsRequest{Filter: `completed && "smoke" in labels`})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(q.GetItems()) != 1 || q.GetItems()[0].GetId() != id {
		t.Fatalf("query matched %d items, want the created one", len(q.GetItems()))
	}
	if q.GetCursor() == 0 {
		t.Fatal("query must report the snapshot cursor for gapless watch resume")
	}

	if _, err := items.UpdateItem(rpcCtx, &taskcorev1.UpdateItemRequest{
		Id:         id,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"mirror.state"}},
		Item:       &taskcorev1.Item{},
	}); err == nil {
		t.Fatal("mirror writes must be rejected (read-only until intents)")
	}

	// Both the create and the completion must arrive on the live watch.
	for i, want := range []taskcorev1.ChangeType{taskcorev1.ChangeType_CHANGE_TYPE_CREATED, taskcorev1.ChangeType_CHANGE_TYPE_UPDATED} {
		select {
		case ev := <-events:
			if ev.GetType() != want {
				t.Fatalf("event %d type = %v, want %v", i, ev.GetType(), want)
			}
			if ev.GetCausedBy().GetRef() != "daemon-test" {
				t.Fatalf("provenance ref = %q, want daemon-test", ev.GetCausedBy().GetRef())
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("watch event %d never arrived", i)
		}
	}

	views := taskcorev1.NewViewServiceClient(conn)
	lv, err := views.ListViews(rpcCtx, &taskcorev1.ListViewsRequest{})
	if err != nil {
		t.Fatalf("list views: %v", err)
	}
	names := map[string]bool{}
	for _, v := range lv.GetViews() {
		names[v.GetName()] = true
	}
	for _, want := range []string{"inbox", "today", "completed"} {
		if !names[want] {
			t.Fatalf("well-known view %q not seeded (got %v)", want, names)
		}
	}
}

// TestSecondDaemon guards the single-daemon-per-dir rule. (Short test name
// on purpose: t.TempDir feeds the unix socket path, and macOS caps those at
// 104 bytes.)
func TestSecondDaemon(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- Run(ctx, Config{Dir: dir}) }()
	t.Cleanup(func() {
		cancel()
		<-errc
	})
	waitFor(t, 5*time.Second, func() bool {
		select {
		case err := <-errc:
			t.Fatalf("first daemon exited early: %v", err)
		default:
		}
		_, err := os.Stat(SocketPath(dir))
		return err == nil
	})

	err := Run(context.Background(), Config{Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second daemon must refuse to start with 'already running', got %v", err)
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition never became true")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
