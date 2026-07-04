package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskpb "todoapp/gen/task"
	"todoapp/gen/task/taskconnect"
	"todoapp/internal/store"
)

// newTestStack serves a real store over a real HTTP server — the full stack
// minus the daemon wiring. The Server is returned too so watch tests can
// observe subscription state.
func newTestStack(t *testing.T) (taskconnect.TaskServiceClient, *Server) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	srv := New(st)
	path, handler := srv.Handler()
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return taskconnect.NewTaskServiceClient(ts.Client(), ts.URL), srv
}

func mustCreate(t *testing.T, tc taskconnect.TaskServiceClient, title string, labels ...string) *taskpb.Task {
	t.Helper()
	res, err := tc.CreateTask(context.Background(), connect.NewRequest(&taskpb.CreateTaskRequest{
		Title:  title,
		Labels: labels,
	}))
	if err != nil {
		t.Fatalf("CreateTask(%q): %v", title, err)
	}
	return res.Msg.GetTask()
}

func wantCode(t *testing.T, err error, code connect.Code) {
	t.Helper()
	var cerr *connect.Error
	if !errors.As(err, &cerr) || cerr.Code() != code {
		t.Fatalf("got error %v, want code %v", err, code)
	}
}

func TestTaskLifecycle(t *testing.T) {
	tc, _ := newTestStack(t)
	ctx := context.Background()

	created := mustCreate(t, tc, "  Buy milk  ", "home", "home", " p1 ")
	if created.GetTitle() != "Buy milk" {
		t.Errorf("title = %q, want trimmed", created.GetTitle())
	}
	if got, want := created.GetLabels(), []string{"home", "p1"}; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("labels = %v, want %v (trimmed, deduped, sorted)", got, want)
	}
	if created.GetRevision() != 1 {
		t.Errorf("revision = %d, want 1", created.GetRevision())
	}

	got, err := tc.GetTask(ctx, connect.NewRequest(&taskpb.GetTaskRequest{Id: created.GetId()}))
	if err != nil || got.Msg.GetTask().GetTitle() != "Buy milk" {
		t.Fatalf("GetTask = %v, %v", got, err)
	}

	// Masked update: set a due date.
	due := timestamppb.New(time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC))
	upd, err := tc.UpdateTask(ctx, connect.NewRequest(&taskpb.UpdateTaskRequest{
		Id:         created.GetId(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"due_time"}},
		Task:       &taskpb.Task{DueTime: due},
	}))
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if upd.Msg.GetTask().GetRevision() != 2 || !upd.Msg.GetTask().GetDueTime().AsTime().Equal(due.AsTime()) {
		t.Errorf("after update: rev=%d due=%v", upd.Msg.GetTask().GetRevision(), upd.Msg.GetTask().GetDueTime())
	}

	// Stale expected_revision → ABORTED.
	_, err = tc.UpdateTask(ctx, connect.NewRequest(&taskpb.UpdateTaskRequest{
		Id:               created.GetId(),
		UpdateMask:       &fieldmaskpb.FieldMask{Paths: []string{"title"}},
		Task:             &taskpb.Task{Title: "x"},
		ExpectedRevision: 1,
	}))
	wantCode(t, err, connect.CodeAborted)

	// Non-updatable path → INVALID_ARGUMENT.
	_, err = tc.UpdateTask(ctx, connect.NewRequest(&taskpb.UpdateTaskRequest{
		Id:         created.GetId(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"source"}},
		Task:       &taskpb.Task{Source: "hax"},
	}))
	wantCode(t, err, connect.CodeInvalidArgument)

	// Complete, then re-open by clearing the masked field.
	now := timestamppb.Now()
	upd, err = tc.UpdateTask(ctx, connect.NewRequest(&taskpb.UpdateTaskRequest{
		Id:         created.GetId(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"completed_time"}},
		Task:       &taskpb.Task{CompletedTime: now},
	}))
	if err != nil || upd.Msg.GetTask().GetCompletedTime() == nil {
		t.Fatalf("complete: %v, task=%v", err, upd.Msg.GetTask())
	}
	upd, err = tc.UpdateTask(ctx, connect.NewRequest(&taskpb.UpdateTaskRequest{
		Id:         created.GetId(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"completed_time"}},
		Task:       &taskpb.Task{},
	}))
	if err != nil || upd.Msg.GetTask().GetCompletedTime() != nil {
		t.Fatalf("re-open: %v, completed=%v", err, upd.Msg.GetTask().GetCompletedTime())
	}

	if _, err := tc.DeleteTask(ctx, connect.NewRequest(&taskpb.DeleteTaskRequest{Id: created.GetId()})); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	_, err = tc.GetTask(ctx, connect.NewRequest(&taskpb.GetTaskRequest{Id: created.GetId()}))
	wantCode(t, err, connect.CodeNotFound)

	_, err = tc.CreateTask(ctx, connect.NewRequest(&taskpb.CreateTaskRequest{Title: "   "}))
	wantCode(t, err, connect.CodeInvalidArgument)
}

func TestListTasksFilterAndPaging(t *testing.T) {
	tc, _ := newTestStack(t)
	ctx := context.Background()

	mustCreate(t, tc, "alpha", "work")
	mustCreate(t, tc, "beta", "home")
	mustCreate(t, tc, "gamma", "work", "urgent")

	res, err := tc.ListTasks(ctx, connect.NewRequest(&taskpb.ListTasksRequest{
		Filter: &taskpb.TaskFilter{LabelsAll: []string{"work"}},
	}))
	if err != nil || len(res.Msg.GetTasks()) != 2 {
		t.Fatalf("labels_all=work: %v tasks, err=%v", len(res.Msg.GetTasks()), err)
	}

	// Keyset paging: walk 1 at a time, titles ascending, no dup/miss.
	var titles []string
	token := ""
	for {
		res, err := tc.ListTasks(ctx, connect.NewRequest(&taskpb.ListTasksRequest{
			OrderBy:   "title asc",
			PageSize:  1,
			PageToken: token,
		}))
		if err != nil {
			t.Fatalf("page: %v", err)
		}
		for _, tk := range res.Msg.GetTasks() {
			titles = append(titles, tk.GetTitle())
		}
		token = res.Msg.GetNextPageToken()
		if token == "" {
			break
		}
	}
	if len(titles) != 3 || titles[0] != "alpha" || titles[1] != "beta" || titles[2] != "gamma" {
		t.Fatalf("paged titles = %v", titles)
	}
}

func TestWatchTasks(t *testing.T) {
	tc, _ := newTestStack(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := tc.WatchTasks(ctx, connect.NewRequest(&taskpb.WatchTasksRequest{}))
	if err != nil {
		t.Fatalf("WatchTasks: %v", err)
	}
	// The first message is the empty handshake: subscription confirmed,
	// nothing else set.
	if ev := recvEvent(t, stream); ev.GetChange() != nil {
		t.Fatalf("handshake = %v, want empty", ev)
	}

	created := mustCreate(t, tc, "watched")
	if _, err := tc.UpdateTask(ctx, connect.NewRequest(&taskpb.UpdateTaskRequest{
		Id:         created.GetId(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"title"}},
		Task:       &taskpb.Task{Title: "watched v2"},
	})); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := tc.DeleteTask(ctx, connect.NewRequest(&taskpb.DeleteTaskRequest{Id: created.GetId()})); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if ev := recvEvent(t, stream); ev.GetTask().GetRevision() != 1 {
		t.Fatalf("event 1 = %v, want create rev 1", ev)
	}
	if ev := recvEvent(t, stream); ev.GetTask().GetTitle() != "watched v2" {
		t.Fatalf("event 2 = %v, want update", ev)
	}
	if ev := recvEvent(t, stream); ev.GetDeletedTaskId() != created.GetId() {
		t.Fatalf("event 3 = %v, want deletion of %s", ev, created.GetId())
	}
}

func recvEvent(t *testing.T, stream *connect.ServerStreamForClient[taskpb.WatchTasksResponse]) *taskpb.WatchTasksResponse {
	t.Helper()
	done := make(chan bool, 1)
	go func() { done <- stream.Receive() }()
	select {
	case ok := <-done:
		if !ok {
			t.Fatalf("stream ended: %v", stream.Err())
		}
		return stream.Msg()
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for watch event")
		return nil
	}
}

func TestUpsertExternalAndLabels(t *testing.T) {
	tc, _ := newTestStack(t)
	ctx := context.Background()

	data, _ := structpb.NewStruct(map[string]any{"location": "office"})
	batch := []*taskpb.ExternalTask{
		{ExternalRef: "ev-1", Title: "Standup", DueTime: timestamppb.New(time.Date(2026, 7, 6, 9, 0, 0, 0, time.UTC)), ExternalData: data},
		{ExternalRef: "ev-2", Title: "Retro"},
	}
	req := &taskpb.UpsertExternalTasksRequest{
		Source:       "ics:test",
		Tasks:        batch,
		ApplyLabels:  []string{"calendar"},
		FullSnapshot: true,
	}
	res, err := tc.UpsertExternalTasks(ctx, connect.NewRequest(req))
	if err != nil || res.Msg.GetCreated() != 2 {
		t.Fatalf("first upsert: %v, res=%v", err, res)
	}

	// Same batch again: everything unchanged, revisions untouched.
	res, err = tc.UpsertExternalTasks(ctx, connect.NewRequest(req))
	if err != nil || res.Msg.GetUnchanged() != 2 || res.Msg.GetCreated() != 0 {
		t.Fatalf("idempotent upsert: %v, res=%v", err, res)
	}

	// The user annotates a synced task; sync must not clobber it.
	list, err := tc.ListTasks(ctx, connect.NewRequest(&taskpb.ListTasksRequest{
		Filter: &taskpb.TaskFilter{Source: strptr("ics:test")},
	}))
	if err != nil || len(list.Msg.GetTasks()) != 2 {
		t.Fatalf("list synced: %v err=%v", list, err)
	}
	target := list.Msg.GetTasks()[0]
	if _, err := tc.UpdateTask(ctx, connect.NewRequest(&taskpb.UpdateTaskRequest{
		Id:         target.GetId(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"labels", "notes"}},
		Task:       &taskpb.Task{Labels: append(target.GetLabels(), "important"), Notes: "bring slides"},
	})); err != nil {
		t.Fatalf("annotate: %v", err)
	}

	// Snapshot shrinks to one event with a changed title.
	res, err = tc.UpsertExternalTasks(ctx, connect.NewRequest(&taskpb.UpsertExternalTasksRequest{
		Source:       "ics:test",
		Tasks:        []*taskpb.ExternalTask{{ExternalRef: target.GetExternalRef(), Title: "Standup (moved)"}},
		ApplyLabels:  []string{"calendar"},
		FullSnapshot: true,
	}))
	if err != nil || res.Msg.GetUpdated() != 1 || res.Msg.GetDeleted() != 1 {
		t.Fatalf("snapshot upsert: %v, res=%v", err, res)
	}
	after, err := tc.GetTask(ctx, connect.NewRequest(&taskpb.GetTaskRequest{Id: target.GetId()}))
	if err != nil {
		t.Fatalf("get after sync: %v", err)
	}
	at := after.Msg.GetTask()
	if at.GetTitle() != "Standup (moved)" || at.GetNotes() != "bring slides" || !hasLabel(at, "important") {
		t.Fatalf("ownership violated: %v", at)
	}

	labels, err := tc.ListLabels(ctx, connect.NewRequest(&taskpb.ListLabelsRequest{}))
	if err != nil {
		t.Fatalf("ListLabels: %v", err)
	}
	found := map[string]int64{}
	for _, lc := range labels.Msg.GetLabels() {
		found[lc.GetLabel()] = lc.GetCount()
	}
	if found["calendar"] != 1 || found["important"] != 1 {
		t.Fatalf("label counts = %v", found)
	}
}

func hasLabel(t *taskpb.Task, want string) bool {
	for _, l := range t.GetLabels() {
		if l == want {
			return true
		}
	}
	return false
}

func strptr(s string) *string { return &s }
