package store

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	taskpb "github.com/serteal/taskd/gen/task"
)

// fixedClockStore opens a store whose clock is pinned to now, so roll-forward
// due dates are exactly assertable.
func fixedClockStore(t *testing.T, now time.Time) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "tasks.db"), func() time.Time { return now })
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// complete marks a recurring task done through the mask, returning the advanced
// live task and its spawned archive.
func complete(t *testing.T, s *Store, id string, at time.Time) (live, spawned *taskpb.Task) {
	t.Helper()
	live, spawned, err := s.Update(context.Background(), id, 0, func(tk *taskpb.Task) error {
		tk.CompletedTime = timestamppb.New(at)
		return nil
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	return live, spawned
}

func TestRollForwardOverdueDailyAdvancesOnce(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	s := fixedClockStore(t, now)
	ctx := context.Background()

	// Due 25h ago: the first occurrence after it (now-1h) is still <= now, so a
	// single fast-forward is needed to land one occurrence in the future.
	due := time.Date(2026, 7, 14, 11, 0, 0, 0, time.UTC)
	task, err := s.Create(ctx, "standup", "notes", []string{"work"}, timestamppb.New(due), "FREQ=DAILY", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	live, spawned := complete(t, s, task.GetId(), now)

	// Live task stays active, advanced to exactly one occurrence in the future.
	if live.GetCompletedTime() != nil {
		t.Errorf("live completed_time = %v, want unset", live.GetCompletedTime())
	}
	if live.GetRecurrence() != "FREQ=DAILY" {
		t.Errorf("live recurrence = %q, want FREQ=DAILY", live.GetRecurrence())
	}
	if live.GetRevision() != 2 {
		t.Errorf("live revision = %d, want 2", live.GetRevision())
	}
	wantNext := time.Date(2026, 7, 16, 11, 0, 0, 0, time.UTC) // due + 2 days, first > now
	if !live.GetDueTime().AsTime().Equal(wantNext) {
		t.Errorf("live due = %v, want %v (advanced once past now)", live.GetDueTime().AsTime(), wantNext)
	}

	// Archive is a frozen, completed, non-recurring copy.
	if spawned == nil {
		t.Fatal("no spawned occurrence")
	}
	if spawned.GetId() == task.GetId() {
		t.Error("archive shares the live task's id")
	}
	if !spawned.GetCompletedTime().AsTime().Equal(now) {
		t.Errorf("archive completed = %v, want %v", spawned.GetCompletedTime().AsTime(), now)
	}
	if !spawned.GetDueTime().AsTime().Equal(due) {
		t.Errorf("archive due = %v, want the old due %v", spawned.GetDueTime().AsTime(), due)
	}
	if spawned.GetRecurrence() != "" {
		t.Errorf("archive recurrence = %q, want cleared", spawned.GetRecurrence())
	}
	if spawned.GetRevision() != 1 {
		t.Errorf("archive revision = %d, want 1", spawned.GetRevision())
	}
	if got := spawned.GetLabels(); !slices.Equal(got, []string{"work"}) {
		t.Errorf("archive labels = %v, want [work]", got)
	}
	if spawned.GetNotes() != "notes" {
		t.Errorf("archive notes = %q, want copied", spawned.GetNotes())
	}

	// Exactly one archive exists — an overdue task completes once, not once per
	// missed occurrence.
	completed := mustList(t, s, Page{Filter: &taskpb.TaskFilter{Completed: boolp(true)}})
	if len(completed) != 1 {
		t.Errorf("completed tasks = %d, want 1: %v", len(completed), titlesOf(completed))
	}
}

func TestRollForwardNoDueAnchorsOnNow(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	s := fixedClockStore(t, now)
	ctx := context.Background()

	task, err := s.Create(ctx, "review", "", nil, nil, "FREQ=WEEKLY", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	live, spawned := complete(t, s, task.GetId(), now)

	wantNext := now.AddDate(0, 0, 7) // anchored on now
	if !live.GetDueTime().AsTime().Equal(wantNext) {
		t.Errorf("live due = %v, want now+7d %v", live.GetDueTime().AsTime(), wantNext)
	}
	if spawned.GetDueTime() != nil {
		t.Errorf("archive due = %v, want unset (task had no due)", spawned.GetDueTime())
	}
}

func TestRollForwardAppliesOtherMaskedFields(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	s := fixedClockStore(t, now)
	ctx := context.Background()

	due := time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC)
	task, err := s.Create(ctx, "old title", "", nil, timestamppb.New(due), "FREQ=DAILY", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Complete AND rename in one request.
	live, spawned, err := s.Update(ctx, task.GetId(), 0, func(tk *taskpb.Task) error {
		tk.Title = "new title"
		tk.CompletedTime = timestamppb.New(now)
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if live.GetTitle() != "new title" {
		t.Errorf("live title = %q, want the masked change applied", live.GetTitle())
	}
	if live.GetCompletedTime() != nil {
		t.Error("live task was completed, want rolled forward")
	}
	wantNext := time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC)
	if !live.GetDueTime().AsTime().Equal(wantNext) {
		t.Errorf("live due = %v, want %v", live.GetDueTime().AsTime(), wantNext)
	}
	if spawned == nil || spawned.GetCompletedTime() == nil {
		t.Fatalf("archive missing or not completed: %v", spawned)
	}
}

func TestRollForwardEarlyCompletionAdvancesOnePeriod(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	s := fixedClockStore(t, now)
	ctx := context.Background()

	// Future due: completing early still advances exactly one period past the due.
	due := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	task, err := s.Create(ctx, "weekly plan", "", nil, timestamppb.New(due), "FREQ=WEEKLY", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	live, _ := complete(t, s, task.GetId(), now)
	wantNext := due.AddDate(0, 0, 7)
	if !live.GetDueTime().AsTime().Equal(wantNext) {
		t.Errorf("live due = %v, want %v (one period after the old due)", live.GetDueTime().AsTime(), wantNext)
	}
}

func TestRecurringClearCompletedIsNormalUpdate(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	task, err := s.Create(ctx, "chore", "", nil, nil, "FREQ=DAILY", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Masking completed_time while it stays unset is a no-op complete-wise: no
	// archive, task stays active, revision still bumps like any update.
	live, spawned, err := s.Update(ctx, task.GetId(), 0, func(tk *taskpb.Task) error {
		tk.CompletedTime = nil
		tk.Notes = "touched"
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if spawned != nil {
		t.Errorf("clearing completed spawned an occurrence: %v", spawned)
	}
	if live.GetCompletedTime() != nil {
		t.Error("task became completed")
	}
	if live.GetNotes() != "touched" || live.GetRevision() != 2 {
		t.Errorf("normal update did not apply: notes=%q rev=%d", live.GetNotes(), live.GetRevision())
	}
}

// TestAddRecurrenceToCompletedTaskDoesNotResurrect is the exact live repro:
// completing a one-off and then editing in a recurrence must NOT un-complete
// the task, advance its due, or spawn an archive. It just records recurrence.
func TestAddRecurrenceToCompletedTaskDoesNotResurrect(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	s := fixedClockStore(t, now)
	ctx := context.Background()

	due := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)
	task, err := s.Create(ctx, "one-off", "", nil, timestamppb.New(due), "", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Complete the one-off.
	completedAt := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	done, spawned, err := s.Update(ctx, task.GetId(), 0, func(tk *taskpb.Task) error {
		tk.CompletedTime = timestamppb.New(completedAt)
		return nil
	})
	if err != nil {
		t.Fatalf("complete one-off: %v", err)
	}
	if spawned != nil || done.GetCompletedTime() == nil {
		t.Fatalf("completing a one-off spawned=%v completed=%v, want a plain completion", spawned, done.GetCompletedTime())
	}

	// Now add a recurrence to the already-completed task.
	live, spawned2, err := s.Update(ctx, task.GetId(), 0, func(tk *taskpb.Task) error {
		tk.Recurrence = "FREQ=WEEKLY"
		return nil
	})
	if err != nil {
		t.Fatalf("add recurrence: %v", err)
	}
	if spawned2 != nil {
		t.Errorf("adding recurrence spawned an occurrence: %v", spawned2)
	}
	if live.GetCompletedTime() == nil || !live.GetCompletedTime().AsTime().Equal(completedAt) {
		t.Errorf("task was resurrected: completed = %v, want it stays completed at %v", live.GetCompletedTime(), completedAt)
	}
	if live.GetRecurrence() != "FREQ=WEEKLY" {
		t.Errorf("recurrence = %q, want FREQ=WEEKLY set", live.GetRecurrence())
	}
	if !live.GetDueTime().AsTime().Equal(due) {
		t.Errorf("due = %v, want unchanged %v (no roll-forward)", live.GetDueTime().AsTime(), due)
	}
	// No archive copy anywhere: exactly one completed task, the original.
	completed := mustList(t, s, Page{Filter: &taskpb.TaskFilter{Completed: boolp(true)}})
	if len(completed) != 1 || completed[0].GetId() != task.GetId() {
		t.Errorf("completed tasks = %v, want only the original", titlesOf(completed))
	}
}

// TestReMaskCompletedOnCompletedRecurringIsPlainUpdate: re-setting completed on
// a task that is ALREADY completed (and carries a recurrence) is a plain
// update, never a second roll-forward.
func TestReMaskCompletedOnCompletedRecurringIsPlainUpdate(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	s := fixedClockStore(t, now)
	ctx := context.Background()

	// A completed recurring task built directly: complete a one-off, then add
	// recurrence (proven above to leave it completed).
	task, err := s.Create(ctx, "chore", "", nil, timestamppb.New(now), "", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, _, err := s.Update(ctx, task.GetId(), 0, func(tk *taskpb.Task) error {
		tk.CompletedTime = timestamppb.New(now)
		return nil
	}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, _, err := s.Update(ctx, task.GetId(), 0, func(tk *taskpb.Task) error {
		tk.Recurrence = "FREQ=DAILY"
		return nil
	}); err != nil {
		t.Fatalf("add recurrence: %v", err)
	}

	// Re-masking completed on the already-completed recurring task: no spawn.
	newDone := now.Add(time.Hour)
	live, spawned, err := s.Update(ctx, task.GetId(), 0, func(tk *taskpb.Task) error {
		tk.CompletedTime = timestamppb.New(newDone)
		return nil
	})
	if err != nil {
		t.Fatalf("re-mask completed: %v", err)
	}
	if spawned != nil {
		t.Errorf("re-masking completed spawned an occurrence: %v", spawned)
	}
	if live.GetCompletedTime() == nil || !live.GetCompletedTime().AsTime().Equal(newDone) {
		t.Errorf("completed = %v, want updated to %v with no roll-forward", live.GetCompletedTime(), newDone)
	}
}

// TestCompleteAndClearRecurrenceCompletesNormally: masking completed_time and
// recurrence="" in the same update completes the task normally (no archive) —
// clearing the rule wins.
func TestCompleteAndClearRecurrenceCompletesNormally(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	s := fixedClockStore(t, now)
	ctx := context.Background()

	task, err := s.Create(ctx, "stop recurring", "", nil, timestamppb.New(now), "FREQ=DAILY", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	live, spawned, err := s.Update(ctx, task.GetId(), 0, func(tk *taskpb.Task) error {
		tk.Recurrence = ""
		tk.CompletedTime = timestamppb.New(now)
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if spawned != nil {
		t.Errorf("clearing recurrence while completing spawned an occurrence: %v", spawned)
	}
	if live.GetCompletedTime() == nil {
		t.Error("task not completed, want a normal completion")
	}
	if live.GetRecurrence() != "" {
		t.Errorf("recurrence = %q, want cleared", live.GetRecurrence())
	}
}

// TestRollForwardArchivesPreMutateDue: when one update masks BOTH completed_time
// and a new due_time, the archive records the PRE-mutate due (the occurrence
// finished) while the live task advances from the POST-mutate due.
func TestRollForwardArchivesPreMutateDue(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	s := fixedClockStore(t, now)
	ctx := context.Background()

	oldDue := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)
	task, err := s.Create(ctx, "series", "", nil, timestamppb.New(oldDue), "FREQ=WEEKLY", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Complete AND move the due in one request; the new due is where the series
	// continues from.
	newDue := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
	completedAt := time.Date(2026, 7, 15, 11, 0, 0, 0, time.UTC)
	live, spawned, err := s.Update(ctx, task.GetId(), 0, func(tk *taskpb.Task) error {
		tk.DueTime = timestamppb.New(newDue)
		tk.CompletedTime = timestamppb.New(completedAt)
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	// Archive records the OLD (pre-mutate) due — the occurrence actually done.
	if spawned == nil {
		t.Fatal("no spawned occurrence")
	}
	if !spawned.GetDueTime().AsTime().Equal(oldDue) {
		t.Errorf("archive due = %v, want the pre-mutate due %v", spawned.GetDueTime().AsTime(), oldDue)
	}
	// Live task advances one week from the NEW due.
	wantNext := newDue.AddDate(0, 0, 7)
	if !live.GetDueTime().AsTime().Equal(wantNext) {
		t.Errorf("live due = %v, want one week after the new due %v", live.GetDueTime().AsTime(), wantNext)
	}
}

// TestRollForwardKeepsLocalWallClockAcrossDST: the store rolls a daily 09:00
// local task across the spring-forward boundary keeping 09:00 local, with the
// absolute instant shifting an hour.
func TestRollForwardKeepsLocalWallClockAcrossDST(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("no America/New_York tzdata: %v", err)
	}
	// Daemon clock fixed just after a 09:00 EST occurrence, in the NY zone.
	now := time.Date(2026, 3, 7, 10, 0, 0, 0, ny)
	s := fixedClockStore(t, now)
	ctx := context.Background()

	due := time.Date(2026, 3, 7, 9, 0, 0, 0, ny)
	task, err := s.Create(ctx, "standup", "", nil, timestamppb.New(due), "FREQ=DAILY", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	live, _ := complete(t, s, task.GetId(), now)

	got := live.GetDueTime().AsTime().In(ny)
	if h, m := got.Hour(), got.Minute(); h != 9 || m != 0 {
		t.Errorf("advanced due local time = %02d:%02d, want 09:00 preserved across DST", h, m)
	}
	// Mar 8 09:00 EDT is UTC 13:00 (only 23h after Mar 7 09:00 EST = UTC 14:00).
	if want := time.Date(2026, 3, 8, 13, 0, 0, 0, time.UTC); !live.GetDueTime().AsTime().Equal(want) {
		t.Errorf("advanced due instant = %v, want %v (UTC shifted by DST)", live.GetDueTime().AsTime(), want)
	}
}

func TestCreateInvalidRecurrenceRejected(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.Create(context.Background(), "t", "", nil, nil, "FREQ=HOURLY", ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("Create with bad recurrence err = %v, want ErrInvalid", err)
	}
}

func boolp(b bool) *bool { return &b }
