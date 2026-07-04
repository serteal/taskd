package store

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskpb "todoapp/gen/task"
)

// fakeClock yields deterministic times: each Now call returns the current
// time and advances by step, so revisions and timestamps are assertable.
// The base time carries sub-millisecond nanos to exercise ms truncation.
type fakeClock struct {
	mu   sync.Mutex
	cur  time.Time
	step time.Duration
}

func newFakeClock() *fakeClock {
	return &fakeClock{
		cur:  time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.UTC),
		step: time.Second,
	}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.cur
	c.cur = c.cur.Add(c.step)
	return t
}

// freeze stops the clock so consecutive writes share a timestamp.
func (c *fakeClock) freeze() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.step = 0
}

func newTestStore(t *testing.T) (*Store, *fakeClock) {
	t.Helper()
	clk := newFakeClock()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "tasks.db"), clk.Now)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s, clk
}

func msTs(ms int64) *timestamppb.Timestamp {
	return timestamppb.New(time.UnixMilli(ms))
}

func mustCreate(t *testing.T, s *Store, title string, labels ...string) *taskpb.Task {
	t.Helper()
	task, err := s.Create(context.Background(), title, "", labels, nil)
	if err != nil {
		t.Fatalf("Create(%q): %v", title, err)
	}
	return task
}

func taskIDs(tasks []*taskpb.Task) []string {
	ids := make([]string, len(tasks))
	for i, task := range tasks {
		ids[i] = task.GetId()
	}
	return ids
}

func TestCreateGetRoundtrip(t *testing.T) {
	s, clk := newTestStore(t)
	ctx := context.Background()

	wantCreate := clk.cur.UnixMilli()
	due := timestamppb.New(time.Date(2026, 2, 1, 0, 0, 0, 987654321, time.UTC))
	created, err := s.Create(ctx, "  Buy milk  ", "2 liters", []string{" home ", "errand", "home", "errand "}, due)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.GetTitle() != "Buy milk" {
		t.Errorf("title = %q, want trimmed %q", created.GetTitle(), "Buy milk")
	}
	if got, want := created.GetLabels(), []string{"errand", "home"}; !slices.Equal(got, want) {
		t.Errorf("labels = %v, want normalized sorted %v", got, want)
	}
	if created.GetRevision() != 1 {
		t.Errorf("revision = %d, want 1", created.GetRevision())
	}
	if created.GetId() == "" {
		t.Error("id is empty")
	}
	if created.GetSource() != "" || created.GetExternalRef() != "" || created.GetExternalData() != nil {
		t.Errorf("external fields set on local task: %v", created)
	}
	// Timestamps are the injected clock truncated to ms.
	if !proto.Equal(created.GetCreateTime(), msTs(wantCreate)) {
		t.Errorf("create_time = %v, want %v", created.GetCreateTime(), msTs(wantCreate))
	}
	if !proto.Equal(created.GetUpdateTime(), msTs(wantCreate)) {
		t.Errorf("update_time = %v, want %v", created.GetUpdateTime(), msTs(wantCreate))
	}
	// The due timestamp is truncated to ms too.
	if !proto.Equal(created.GetDueTime(), msTs(due.AsTime().UnixMilli())) {
		t.Errorf("due_time = %v, want ms-truncated %v", created.GetDueTime(), msTs(due.AsTime().UnixMilli()))
	}

	got, err := s.Get(ctx, created.GetId())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !proto.Equal(got, created) {
		t.Errorf("Get = %v, want the task Create returned: %v", got, created)
	}
}

func TestCreateNilDue(t *testing.T) {
	s, _ := newTestStore(t)
	task := mustCreate(t, s, "no due")
	if task.GetDueTime() != nil {
		t.Errorf("due_time = %v, want nil", task.GetDueTime())
	}
	got, err := s.Get(context.Background(), task.GetId())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.GetDueTime() != nil {
		t.Errorf("Get due_time = %v, want nil", got.GetDueTime())
	}
}

func TestCreateEmptyTitle(t *testing.T) {
	s, _ := newTestStore(t)
	for _, title := range []string{"", "   ", "\t\n"} {
		if _, err := s.Create(context.Background(), title, "", nil, nil); !errors.Is(err, ErrInvalid) {
			t.Errorf("Create(%q) err = %v, want ErrInvalid", title, err)
		}
	}
}

func TestCreateEmptyLabel(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.Create(context.Background(), "t", "", []string{"ok", "  "}, nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("Create with blank label err = %v, want ErrInvalid", err)
	}
}

func TestGetNotFound(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.Get(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get err = %v, want ErrNotFound", err)
	}
}

func TestUpdateHappyPath(t *testing.T) {
	s, clk := newTestStore(t)
	ctx := context.Background()
	task := mustCreate(t, s, "before", "old")

	wantUpdate := clk.cur.UnixMilli()
	due := msTs(5000)
	done := msTs(6000)
	updated, err := s.Update(ctx, task.GetId(), task.GetRevision(), func(tk *taskpb.Task) error {
		tk.Title = "  after  "
		tk.Notes = "new notes"
		tk.Labels = []string{" b ", "a", "b"}
		tk.DueTime = due
		tk.CompletedTime = done
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.GetTitle() != "after" {
		t.Errorf("title = %q, want %q", updated.GetTitle(), "after")
	}
	if updated.GetNotes() != "new notes" {
		t.Errorf("notes = %q", updated.GetNotes())
	}
	if got, want := updated.GetLabels(), []string{"a", "b"}; !slices.Equal(got, want) {
		t.Errorf("labels = %v, want %v", got, want)
	}
	if !proto.Equal(updated.GetDueTime(), due) || !proto.Equal(updated.GetCompletedTime(), done) {
		t.Errorf("due/completed = %v/%v", updated.GetDueTime(), updated.GetCompletedTime())
	}
	if updated.GetRevision() != 2 {
		t.Errorf("revision = %d, want 2", updated.GetRevision())
	}
	if !proto.Equal(updated.GetCreateTime(), task.GetCreateTime()) {
		t.Errorf("create_time changed: %v -> %v", task.GetCreateTime(), updated.GetCreateTime())
	}
	if !proto.Equal(updated.GetUpdateTime(), msTs(wantUpdate)) {
		t.Errorf("update_time = %v, want %v", updated.GetUpdateTime(), msTs(wantUpdate))
	}

	got, err := s.Get(ctx, task.GetId())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !proto.Equal(got, updated) {
		t.Errorf("Get = %v, want %v", got, updated)
	}
}

func TestUpdateRevisionMismatch(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	task := mustCreate(t, s, "t")

	mutateRan := false
	_, err := s.Update(ctx, task.GetId(), 99, func(*taskpb.Task) error {
		mutateRan = true
		return nil
	})
	if !errors.Is(err, ErrRevisionMismatch) {
		t.Errorf("err = %v, want ErrRevisionMismatch", err)
	}
	if mutateRan {
		t.Error("mutate ran despite revision mismatch")
	}

	// Zero means last-write-wins.
	if _, err := s.Update(ctx, task.GetId(), 0, func(*taskpb.Task) error { return nil }); err != nil {
		t.Errorf("Update with expectedRevision 0: %v", err)
	}
}

func TestUpdateNotFound(t *testing.T) {
	s, _ := newTestStore(t)
	_, err := s.Update(context.Background(), "nope", 0, func(*taskpb.Task) error { return nil })
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestUpdateMutateErrorPropagates(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	task := mustCreate(t, s, "t")

	boom := errors.New("boom")
	_, err := s.Update(ctx, task.GetId(), 0, func(*taskpb.Task) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the mutate error", err)
	}
	// The write was rolled back.
	got, err := s.Get(ctx, task.GetId())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.GetRevision() != 1 {
		t.Errorf("revision = %d after failed mutate, want 1", got.GetRevision())
	}
}

func TestUpdateMutateSeesCurrentState(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	task := mustCreate(t, s, "current title", "x")

	_, err := s.Update(ctx, task.GetId(), 0, func(tk *taskpb.Task) error {
		if tk.GetTitle() != "current title" || !slices.Equal(tk.GetLabels(), []string{"x"}) {
			t.Errorf("mutate saw %q %v, want stored state", tk.GetTitle(), tk.GetLabels())
		}
		if tk.GetRevision() != 1 {
			t.Errorf("mutate saw revision %d, want 1", tk.GetRevision())
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
}

func TestUpdateImmutableFieldsIgnored(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	task := mustCreate(t, s, "t")

	evil, err := structpb.NewStruct(map[string]any{"hax": true})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := s.Update(ctx, task.GetId(), 0, func(tk *taskpb.Task) error {
		tk.Id = "evil-id"
		tk.Source = "evil"
		tk.ExternalRef = "evil-ref"
		tk.ExternalData = evil
		tk.Revision = 99
		tk.CreateTime = msTs(1)
		tk.Notes = "kept"
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.GetId() != task.GetId() {
		t.Errorf("id = %q, want %q", updated.GetId(), task.GetId())
	}
	if updated.GetSource() != "" || updated.GetExternalRef() != "" || updated.GetExternalData() != nil {
		t.Errorf("external fields mutated: %v", updated)
	}
	if updated.GetRevision() != 2 {
		t.Errorf("revision = %d, want 2 (mutation ignored, increment applied)", updated.GetRevision())
	}
	if !proto.Equal(updated.GetCreateTime(), task.GetCreateTime()) {
		t.Errorf("create_time mutated: %v", updated.GetCreateTime())
	}
	if updated.GetNotes() != "kept" {
		t.Errorf("notes = %q, want mutable field applied", updated.GetNotes())
	}
	got, err := s.Get(ctx, task.GetId())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !proto.Equal(got, updated) {
		t.Errorf("stored task %v differs from returned %v", got, updated)
	}
}

func TestUpdateInvalid(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	task := mustCreate(t, s, "t")

	_, err := s.Update(ctx, task.GetId(), 0, func(tk *taskpb.Task) error {
		tk.Title = "   "
		return nil
	})
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("blank title err = %v, want ErrInvalid", err)
	}
	_, err = s.Update(ctx, task.GetId(), 0, func(tk *taskpb.Task) error {
		tk.Labels = []string{""}
		return nil
	})
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("blank label err = %v, want ErrInvalid", err)
	}
}

func TestDeleteCascadesLabels(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	doomed := mustCreate(t, s, "doomed", "shared", "only-doomed")
	mustCreate(t, s, "survivor", "shared")

	if err := s.Delete(ctx, doomed.GetId()); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, doomed.GetId()); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after delete err = %v, want ErrNotFound", err)
	}
	labels, err := s.ListLabels(ctx, true)
	if err != nil {
		t.Fatalf("ListLabels: %v", err)
	}
	if len(labels) != 1 || labels[0].GetLabel() != "shared" || labels[0].GetCount() != 1 {
		t.Errorf("labels after cascade = %v, want only shared:1", labels)
	}
}

func TestDeleteNotFound(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.Delete(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
