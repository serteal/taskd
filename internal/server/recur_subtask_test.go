package server

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskpb "github.com/serteal/taskd/gen/task"
	"github.com/serteal/taskd/gen/task/taskconnect"
)

func createTask(t *testing.T, tc taskconnect.TaskServiceClient, req *taskpb.CreateTaskRequest) *taskpb.Task {
	t.Helper()
	res, err := tc.CreateTask(context.Background(), connect.NewRequest(req))
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return res.Msg.GetTask()
}

func TestRecurrenceRollForwardResponse(t *testing.T) {
	tc, _ := newTestStack(t)
	ctx := context.Background()

	due := timestamppb.New(time.Now().Add(-48 * time.Hour))
	created := createTask(t, tc, &taskpb.CreateTaskRequest{
		Title: "standup", Labels: []string{"work"}, DueTime: due, Recurrence: "FREQ=DAILY",
	})
	if created.GetRecurrence() != "FREQ=DAILY" {
		t.Fatalf("created recurrence = %q", created.GetRecurrence())
	}

	res, err := tc.UpdateTask(ctx, connect.NewRequest(&taskpb.UpdateTaskRequest{
		Id:         created.GetId(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"completed_time"}},
		Task:       &taskpb.Task{CompletedTime: timestamppb.Now()},
	}))
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	live := res.Msg.GetTask()
	if live.GetCompletedTime() != nil {
		t.Errorf("live task completed, want rolled forward: %v", live.GetCompletedTime())
	}
	if live.GetRecurrence() != "FREQ=DAILY" || live.GetRevision() != 2 {
		t.Errorf("live task = recurrence %q rev %d, want FREQ=DAILY / 2", live.GetRecurrence(), live.GetRevision())
	}
	if !live.GetDueTime().AsTime().After(time.Now()) {
		t.Errorf("live due = %v, want a future occurrence", live.GetDueTime().AsTime())
	}

	spawned := res.Msg.GetSpawnedOccurrence()
	if spawned == nil {
		t.Fatal("response has no spawned_occurrence")
	}
	if spawned.GetCompletedTime() == nil || spawned.GetRecurrence() != "" || spawned.GetRevision() != 1 {
		t.Errorf("archive = completed %v recurrence %q rev %d, want completed / '' / 1",
			spawned.GetCompletedTime(), spawned.GetRecurrence(), spawned.GetRevision())
	}
	if spawned.GetId() == live.GetId() {
		t.Error("archive shares the live task id")
	}
}

func TestRecurrenceRollForwardPublishesBoth(t *testing.T) {
	tc, _ := newTestStack(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	created := createTask(t, tc, &taskpb.CreateTaskRequest{Title: "chore", Recurrence: "FREQ=DAILY"})

	stream, err := tc.WatchTasks(ctx, connect.NewRequest(&taskpb.WatchTasksRequest{}))
	if err != nil {
		t.Fatalf("WatchTasks: %v", err)
	}
	if ev := recvEvent(t, stream); ev.GetChange() != nil {
		t.Fatalf("handshake = %v, want empty", ev)
	}

	if _, err := tc.UpdateTask(ctx, connect.NewRequest(&taskpb.UpdateTaskRequest{
		Id:         created.GetId(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"completed_time"}},
		Task:       &taskpb.Task{CompletedTime: timestamppb.Now()},
	})); err != nil {
		t.Fatalf("complete: %v", err)
	}

	// Both rows publish: the advanced live task and the archived occurrence.
	var sawLive, sawArchive bool
	for range 2 {
		tk := recvEvent(t, stream).GetTask()
		if tk == nil {
			t.Fatalf("expected a task event")
		}
		switch {
		case tk.GetId() == created.GetId() && tk.GetCompletedTime() == nil && tk.GetRevision() == 2:
			sawLive = true
		case tk.GetId() != created.GetId() && tk.GetCompletedTime() != nil && tk.GetRecurrence() == "":
			sawArchive = true
		}
	}
	if !sawLive || !sawArchive {
		t.Errorf("watch events: sawLive=%v sawArchive=%v, want both", sawLive, sawArchive)
	}
}

func TestRecurrenceValidation(t *testing.T) {
	tc, _ := newTestStack(t)
	ctx := context.Background()

	// Malformed rule on create.
	_, err := tc.CreateTask(ctx, connect.NewRequest(&taskpb.CreateTaskRequest{Title: "x", Recurrence: "FREQ=HOURLY"}))
	wantCode(t, err, connect.CodeInvalidArgument)

	// Malformed rule on update.
	task := mustCreate(t, tc, "y")
	_, err = tc.UpdateTask(ctx, connect.NewRequest(&taskpb.UpdateTaskRequest{
		Id:         task.GetId(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"recurrence"}},
		Task:       &taskpb.Task{Recurrence: "not a rule"},
	}))
	wantCode(t, err, connect.CodeInvalidArgument)

	// Recurrence on a synced task is rejected.
	if _, err := tc.UpsertExternalTasks(ctx, connect.NewRequest(&taskpb.UpsertExternalTasksRequest{
		Source: "github",
		Tasks:  []*taskpb.ExternalTask{{ExternalRef: "pr/1", Title: "PR"}},
	})); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	src := "github"
	list, err := tc.ListTasks(ctx, connect.NewRequest(&taskpb.ListTasksRequest{Filter: &taskpb.TaskFilter{Source: &src}}))
	if err != nil || len(list.Msg.GetTasks()) != 1 {
		t.Fatalf("list synced: %v, err %v", list, err)
	}
	_, err = tc.UpdateTask(ctx, connect.NewRequest(&taskpb.UpdateTaskRequest{
		Id:         list.Msg.GetTasks()[0].GetId(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"recurrence"}},
		Task:       &taskpb.Task{Recurrence: "FREQ=DAILY"},
	}))
	wantCode(t, err, connect.CodeInvalidArgument)

	// Clearing completed on a recurring task is a no-op complete-wise.
	rec := createTask(t, tc, &taskpb.CreateTaskRequest{Title: "recurs", Recurrence: "FREQ=DAILY"})
	res, err := tc.UpdateTask(ctx, connect.NewRequest(&taskpb.UpdateTaskRequest{
		Id:         rec.GetId(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"completed_time"}},
		Task:       &taskpb.Task{},
	}))
	if err != nil {
		t.Fatalf("clear completed: %v", err)
	}
	if res.Msg.GetSpawnedOccurrence() != nil {
		t.Errorf("clearing completed spawned an occurrence: %v", res.Msg.GetSpawnedOccurrence())
	}
	if res.Msg.GetTask().GetCompletedTime() != nil {
		t.Errorf("task became completed: %v", res.Msg.GetTask().GetCompletedTime())
	}
}

func TestSubtaskValidationCodes(t *testing.T) {
	tc, _ := newTestStack(t)
	ctx := context.Background()

	parent := mustCreate(t, tc, "parent")
	child := createTask(t, tc, &taskpb.CreateTaskRequest{Title: "child", ParentId: parent.GetId()})

	// Missing parent -> NOT_FOUND.
	_, err := tc.CreateTask(ctx, connect.NewRequest(&taskpb.CreateTaskRequest{Title: "x", ParentId: "no-such"}))
	wantCode(t, err, connect.CodeNotFound)

	// Parent is itself a subtask -> INVALID_ARGUMENT (depth downward).
	_, err = tc.CreateTask(ctx, connect.NewRequest(&taskpb.CreateTaskRequest{Title: "gc", ParentId: child.GetId()}))
	wantCode(t, err, connect.CodeInvalidArgument)

	// Self-parent -> INVALID_ARGUMENT.
	_, err = tc.UpdateTask(ctx, connect.NewRequest(&taskpb.UpdateTaskRequest{
		Id:         parent.GetId(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"parent_id"}},
		Task:       &taskpb.Task{ParentId: parent.GetId()},
	}))
	wantCode(t, err, connect.CodeInvalidArgument)

	// A task that has children can't be given a parent -> INVALID_ARGUMENT.
	other := mustCreate(t, tc, "other")
	_, err = tc.UpdateTask(ctx, connect.NewRequest(&taskpb.UpdateTaskRequest{
		Id:         parent.GetId(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"parent_id"}},
		Task:       &taskpb.Task{ParentId: other.GetId()},
	}))
	wantCode(t, err, connect.CodeInvalidArgument)

	// A synced parent is allowed.
	if _, err := tc.UpsertExternalTasks(ctx, connect.NewRequest(&taskpb.UpsertExternalTasksRequest{
		Source: "github",
		Tasks:  []*taskpb.ExternalTask{{ExternalRef: "pr/9", Title: "PR"}},
	})); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	src := "github"
	list, _ := tc.ListTasks(ctx, connect.NewRequest(&taskpb.ListTasksRequest{Filter: &taskpb.TaskFilter{Source: &src}}))
	syncedID := list.Msg.GetTasks()[0].GetId()
	if _, err := tc.CreateTask(ctx, connect.NewRequest(&taskpb.CreateTaskRequest{Title: "under PR", ParentId: syncedID})); err != nil {
		t.Errorf("create under synced parent: %v, want ok", err)
	}
}

func TestDeleteReparentsAndPublishes(t *testing.T) {
	tc, _ := newTestStack(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	parent := mustCreate(t, tc, "parent")
	child := createTask(t, tc, &taskpb.CreateTaskRequest{Title: "child", ParentId: parent.GetId()})

	stream, err := tc.WatchTasks(ctx, connect.NewRequest(&taskpb.WatchTasksRequest{}))
	if err != nil {
		t.Fatalf("WatchTasks: %v", err)
	}
	if ev := recvEvent(t, stream); ev.GetChange() != nil {
		t.Fatalf("handshake = %v, want empty", ev)
	}

	if _, err := tc.DeleteTask(ctx, connect.NewRequest(&taskpb.DeleteTaskRequest{Id: parent.GetId()})); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Expect a deletion for the parent and a re-parent update for the child.
	var sawDelete, sawReparent bool
	for range 2 {
		ev := recvEvent(t, stream)
		switch {
		case ev.GetDeletedTaskId() == parent.GetId():
			sawDelete = true
		case ev.GetTask().GetId() == child.GetId() && ev.GetTask().GetParentId() == "":
			sawReparent = true
		}
	}
	if !sawDelete || !sawReparent {
		t.Errorf("delete events: sawDelete=%v sawReparent=%v, want both", sawDelete, sawReparent)
	}
}

func TestPruneReparentsAndPublishes(t *testing.T) {
	tc, _ := newTestStack(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := tc.UpsertExternalTasks(ctx, connect.NewRequest(&taskpb.UpsertExternalTasksRequest{
		Source: "github",
		Tasks:  []*taskpb.ExternalTask{{ExternalRef: "pr/1", Title: "PR"}},
	})); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	src := "github"
	list, _ := tc.ListTasks(ctx, connect.NewRequest(&taskpb.ListTasksRequest{Filter: &taskpb.TaskFilter{Source: &src}}))
	syncedParent := list.Msg.GetTasks()[0]
	child := createTask(t, tc, &taskpb.CreateTaskRequest{Title: "checklist", ParentId: syncedParent.GetId()})

	stream, err := tc.WatchTasks(ctx, connect.NewRequest(&taskpb.WatchTasksRequest{}))
	if err != nil {
		t.Fatalf("WatchTasks: %v", err)
	}
	if ev := recvEvent(t, stream); ev.GetChange() != nil {
		t.Fatalf("handshake = %v, want empty", ev)
	}

	// A full snapshot that drops the parent prunes it; the child re-parents.
	if _, err := tc.UpsertExternalTasks(ctx, connect.NewRequest(&taskpb.UpsertExternalTasksRequest{
		Source: "github", Tasks: nil, FullSnapshot: true,
	})); err != nil {
		t.Fatalf("prune upsert: %v", err)
	}
	var sawDelete, sawReparent bool
	for range 2 {
		ev := recvEvent(t, stream)
		switch {
		case ev.GetDeletedTaskId() == syncedParent.GetId():
			sawDelete = true
		case ev.GetTask().GetId() == child.GetId() && ev.GetTask().GetParentId() == "":
			sawReparent = true
		}
	}
	if !sawDelete || !sawReparent {
		t.Errorf("prune events: sawDelete=%v sawReparent=%v, want both", sawDelete, sawReparent)
	}
}
