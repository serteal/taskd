package store_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/store"
)

const (
	queued    = taskcorev1.IntentState_INTENT_STATE_QUEUED
	inflight  = taskcorev1.IntentState_INTENT_STATE_INFLIGHT
	confirmed = taskcorev1.IntentState_INTENT_STATE_CONFIRMED
	failed    = taskcorev1.IntentState_INTENT_STATE_FAILED
	discarded = taskcorev1.IntentState_INTENT_STATE_DISCARDED
)

// rec builds a minimal intent record; the caller sets timestamps/params.
func rec(id, item string, state taskcorev1.IntentState) *taskcorev1.IntentRecord {
	return &taskcorev1.IntentRecord{
		Id:     id,
		ItemId: item,
		Intent: "rename",
		State:  state,
	}
}

// enqueue stamps created_at from the fake clock (so ordering tests get a
// distinct, advancing created_at) and enqueues r.
func (e *env) enqueue(t *testing.T, r *taskcorev1.IntentRecord) {
	t.Helper()
	if r.CreatedAt == nil {
		r.CreatedAt = timestamppb.New(e.fake.Now())
	}
	if err := e.st.EnqueueIntent(context.Background(), r); err != nil {
		t.Fatalf("EnqueueIntent(%s): %v", r.GetId(), err)
	}
}

func intentIDs(recs []*taskcorev1.IntentRecord) []string {
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.GetId())
	}
	return out
}

func TestIntentEnqueueGetRoundTrip(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	params, err := structpb.NewStruct(map[string]any{"title": "new name"})
	if err != nil {
		t.Fatalf("NewStruct: %v", err)
	}
	r := rec("i1", "item1", queued)
	r.ConnectorInstance = "linear@work"
	r.Params = params
	r.Attempts = 2
	r.LastError = "429 rate limited"
	r.CreatedAt = timestamppb.New(e.fake.Now())
	r.NextAttemptAt = timestamppb.New(e.fake.Now().Add(time.Hour))

	if err := e.st.EnqueueIntent(ctx, r); err != nil {
		t.Fatalf("EnqueueIntent: %v", err)
	}

	want := proto.Clone(r).(*taskcorev1.IntentRecord)
	want.UpdatedAt = timestamppb.New(e.fake.Now()) // stamped by the store

	got, err := e.st.GetIntent(ctx, "i1")
	if err != nil {
		t.Fatalf("GetIntent: %v", err)
	}
	if !proto.Equal(got, want) {
		t.Errorf("round trip mismatch:\n got: %v\nwant: %v", got, want)
	}
	// The store stamps updated_at on a clone, never the caller's record.
	if r.GetUpdatedAt() != nil {
		t.Errorf("EnqueueIntent mutated the caller's updated_at: %v", r.GetUpdatedAt())
	}

	if _, err := e.st.GetIntent(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetIntent(missing) = %v, want ErrNotFound", err)
	}

	// A duplicate id must fail (the primary-key constraint).
	if err := e.st.EnqueueIntent(ctx, rec("i1", "item1", queued)); err == nil {
		t.Errorf("duplicate id enqueue succeeded, want error")
	}
	// Empty id / item_id are rejected.
	if err := e.st.EnqueueIntent(ctx, rec("", "item1", queued)); err == nil {
		t.Errorf("empty id enqueue succeeded, want error")
	}
	if err := e.st.EnqueueIntent(ctx, rec("i2", "", queued)); err == nil {
		t.Errorf("empty item_id enqueue succeeded, want error")
	}
}

func TestUpdateIntent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	e.enqueue(t, rec("i1", "item1", queued))

	if err := e.st.UpdateIntent(ctx, rec("ghost", "item1", failed)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("UpdateIntent(missing) = %v, want ErrNotFound", err)
	}

	updatedAt := e.fake.Advance(time.Minute)
	got, err := e.st.GetIntent(ctx, "i1")
	if err != nil {
		t.Fatalf("GetIntent: %v", err)
	}
	got.State = failed
	got.LastError = "boom"
	if err := e.st.UpdateIntent(ctx, got); err != nil {
		t.Fatalf("UpdateIntent: %v", err)
	}

	after, err := e.st.GetIntent(ctx, "i1")
	if err != nil {
		t.Fatalf("GetIntent: %v", err)
	}
	if after.GetState() != failed {
		t.Errorf("state = %v, want FAILED", after.GetState())
	}
	if after.GetLastError() != "boom" {
		t.Errorf("last_error = %q, want boom", after.GetLastError())
	}
	if !after.GetUpdatedAt().AsTime().Equal(updatedAt) {
		t.Errorf("updated_at = %v, want %v (stamped by the store)", after.GetUpdatedAt().AsTime(), updatedAt)
	}

	// The state column tracked the write: the record surfaces under FAILED and
	// no longer under QUEUED.
	if list, _ := e.st.ListIntents(ctx, []taskcorev1.IntentState{failed}, "", 0); !slices.Equal(intentIDs(list), []string{"i1"}) {
		t.Errorf("ListIntents[FAILED] = %v, want [i1]", intentIDs(list))
	}
	if list, _ := e.st.ListIntents(ctx, []taskcorev1.IntentState{queued}, "", 0); len(list) != 0 {
		t.Errorf("ListIntents[QUEUED] = %v, want empty", intentIDs(list))
	}
}

func TestListIntents(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	// created_at advances with each enqueue: q < i < f < c < d.
	e.enqueue(t, rec("q", "itemA", queued))
	e.fake.Advance(time.Minute)
	e.enqueue(t, rec("i", "itemA", inflight))
	e.fake.Advance(time.Minute)
	e.enqueue(t, rec("f", "itemB", failed))
	e.fake.Advance(time.Minute)
	e.enqueue(t, rec("c", "itemA", confirmed))
	e.fake.Advance(time.Minute)
	e.enqueue(t, rec("d", "itemB", discarded))

	// Default states are QUEUED+INFLIGHT+FAILED — CONFIRMED/DISCARDED excluded
	// — newest first.
	def, err := e.st.ListIntents(ctx, nil, "", 0)
	if err != nil {
		t.Fatalf("ListIntents(default): %v", err)
	}
	if got, want := intentIDs(def), []string{"f", "i", "q"}; !slices.Equal(got, want) {
		t.Errorf("default states = %v, want %v", got, want)
	}

	// Explicit single-state filter.
	only, _ := e.st.ListIntents(ctx, []taskcorev1.IntentState{failed}, "", 0)
	if got, want := intentIDs(only), []string{"f"}; !slices.Equal(got, want) {
		t.Errorf("[FAILED] filter = %v, want %v", got, want)
	}

	// Item filter narrows to itemA (default states still drop confirmed c).
	byItem, _ := e.st.ListIntents(ctx, nil, "itemA", 0)
	if got, want := intentIDs(byItem), []string{"i", "q"}; !slices.Equal(got, want) {
		t.Errorf("itemA filter = %v, want %v", got, want)
	}

	// Limit keeps the newest.
	limited, _ := e.st.ListIntents(ctx, nil, "", 1)
	if got, want := intentIDs(limited), []string{"f"}; !slices.Equal(got, want) {
		t.Errorf("limit 1 = %v, want %v", got, want)
	}
}

func TestDueIntents(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	base := e.fake.Now()

	// Unset next_attempt_at is always due; oldest created_at first.
	e.enqueue(t, rec("a", "item1", queued))
	e.fake.Advance(time.Minute)
	past := rec("b", "item1", queued)
	past.NextAttemptAt = timestamppb.New(base.Add(-time.Hour)) // past → due
	e.enqueue(t, past)
	e.fake.Advance(time.Minute)
	future := rec("c", "item1", queued)
	future.NextAttemptAt = timestamppb.New(base.Add(time.Hour)) // future → not due
	e.enqueue(t, future)
	e.fake.Advance(time.Minute)
	// Non-QUEUED states are never due, even with no next_attempt_at.
	e.enqueue(t, rec("d", "item1", inflight))
	e.enqueue(t, rec("e", "item1", failed))

	due, err := e.st.DueIntents(ctx, base, 0)
	if err != nil {
		t.Fatalf("DueIntents: %v", err)
	}
	if got, want := intentIDs(due), []string{"a", "b"}; !slices.Equal(got, want) {
		t.Errorf("due = %v, want %v (oldest-first, QUEUED and arrived only)", got, want)
	}
}

func TestRequeueStaleInflight(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	old := rec("old", "item1", inflight)
	old.LastError = "connection reset"
	e.enqueue(t, old) // updated_at stamped at T0
	t1 := e.fake.Advance(time.Hour)
	e.enqueue(t, rec("new", "item1", inflight)) // updated_at stamped at T0+1h
	e.enqueue(t, rec("queued", "item1", queued))

	// Cutoff sits between the two INFLIGHT stamps: only "old" is stale.
	cutoff := t1.Add(-30 * time.Minute)
	n, err := e.st.RequeueStaleInflight(ctx, cutoff)
	if err != nil {
		t.Fatalf("RequeueStaleInflight: %v", err)
	}
	if n != 1 {
		t.Fatalf("requeued count = %d, want 1", n)
	}

	gotOld, err := e.st.GetIntent(ctx, "old")
	if err != nil {
		t.Fatalf("GetIntent(old): %v", err)
	}
	if gotOld.GetState() != queued {
		t.Errorf("old state = %v, want QUEUED", gotOld.GetState())
	}
	if gotOld.GetLastError() != "connection reset" {
		t.Errorf("old last_error = %q, want preserved", gotOld.GetLastError())
	}

	gotNew, err := e.st.GetIntent(ctx, "new")
	if err != nil {
		t.Fatalf("GetIntent(new): %v", err)
	}
	if gotNew.GetState() != inflight {
		t.Errorf("new state = %v, want still INFLIGHT", gotNew.GetState())
	}
}

// TestIntentMigrationApplied proves migration 004 runs on a fresh Open: the
// intent methods can only work if the intents table exists (store_test cannot
// reach the raw db handle, so behavior is the assertion).
func TestIntentMigrationApplied(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	if err := e.st.EnqueueIntent(ctx, rec("m1", "item1", queued)); err != nil {
		t.Fatalf("EnqueueIntent on fresh store: %v (migration 004 not applied?)", err)
	}
	if _, err := e.st.GetIntent(ctx, "m1"); err != nil {
		t.Fatalf("GetIntent on fresh store: %v", err)
	}
}
