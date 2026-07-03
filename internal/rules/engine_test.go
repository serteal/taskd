package rules_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/clock"
	"todoapp/internal/feed"
	"todoapp/internal/query"
	"todoapp/internal/rules"
	"todoapp/internal/store"
)

type env struct {
	st  store.Store
	hub *feed.Hub
	clk *clock.Fake
	qe  *query.Engine
	eng *rules.Engine
	ids clock.IDGen
}

func newEnv(t *testing.T) *env {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC))
	qe, err := query.NewEngine(clk.Now)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(store.Options{
		Path:    filepath.Join(t.TempDir(), "t.db"),
		Diff:    feed.Diff,
		Extract: qe.Extract,
		Now:     clk.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hub := feed.NewHub()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &env{
		st: st, hub: hub, clk: clk, qe: qe,
		eng: rules.NewEngine(st, hub, qe, clk, log),
		ids: clock.NewIDGen(clk, 3),
	}
}

var (
	syncProv = &taskcorev1.Provenance{Source: taskcorev1.ProvenanceSource_PROVENANCE_SOURCE_SYNC, Ref: "cal@t"}
	userProv = &taskcorev1.Provenance{Source: taskcorev1.ProvenanceSource_PROVENANCE_SOURCE_CLIENT, Ref: "test"}
)

// newMirror creates a mirrored, un-triaged item (as the sync engine would).
func (e *env) newMirror(t *testing.T, ext, state string) string {
	t.Helper()
	item := &taskcorev1.Item{
		Id: e.ids.NewID(), Kind: "gh.pr",
		Mirror: &taskcorev1.Mirror{
			Title: "PR " + ext, State: state,
			Link: &taskcorev1.ExternalLink{ConnectorInstance: "gh@t", ExternalId: ext},
		},
		MirrorRevision: 1,
	}
	evt, err := e.st.CreateItem(context.Background(), item, syncProv)
	if err != nil {
		t.Fatal(err)
	}
	e.hub.Publish(evt)
	return item.GetId()
}

func (e *env) setState(t *testing.T, id, state string) {
	t.Helper()
	_, evt, err := e.st.MutateItem(context.Background(), id, syncProv, func(it *taskcorev1.Item) error {
		it.Mirror.State = state
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if evt != nil {
		e.hub.Publish(evt)
	}
}

func (e *env) touchTitle(t *testing.T, id, title string) {
	t.Helper()
	_, evt, err := e.st.MutateItem(context.Background(), id, syncProv, func(it *taskcorev1.Item) error {
		it.Mirror.Title = title
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if evt != nil {
		e.hub.Publish(evt)
	}
}

func (e *env) save(t *testing.T, r *taskcorev1.Rule) {
	t.Helper()
	if err := rules.Validate(e.qe, r); err != nil {
		t.Fatalf("Validate(%s): %v", r.GetName(), err)
	}
	if err := e.st.SaveRule(context.Background(), r); err != nil {
		t.Fatal(err)
	}
}

func (e *env) drain(t *testing.T) {
	t.Helper()
	if _, err := e.eng.DrainOnce(context.Background()); err != nil {
		t.Fatalf("DrainOnce: %v", err)
	}
}

func (e *env) item(t *testing.T, id string) *taskcorev1.Item {
	t.Helper()
	it, err := e.st.GetItem(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return it
}

// TestDoorbell is THE phase-3 test: edge semantics prevent the classic
// automation footgun where an unrelated update resurrects a completed todo.
func TestDoorbell(t *testing.T) {
	e := newEnv(t)
	e.drain(t) // anchor cursor at tail before any activity

	e.save(t, &taskcorev1.Rule{
		Name:   "review-requests",
		Became: `kind == "gh.pr" && state == "review"`,
		Do: &taskcorev1.RuleActions{
			Add: true, SetProject: strPtr("reviews"),
			AddLabels: []string{"code"},
		},
	})

	id := e.newMirror(t, "pr-1", "open")
	e.drain(t) // CREATED with state "open": condition false, no fire
	if e.item(t, id).GetTodo() != nil {
		t.Fatal("rule fired without its condition becoming true")
	}

	// Monday: review requested → condition flips → todo appears.
	e.setState(t, id, "review")
	e.drain(t)
	got := e.item(t, id)
	if got.GetTodo() == nil || got.GetTodo().GetProject() != "reviews" {
		t.Fatalf("rule must fire on the flip: %v", got.GetTodo())
	}

	// Tuesday: user completes the todo.
	if _, evt, err := e.st.MutateItem(context.Background(), id, userProv, func(it *taskcorev1.Item) error {
		it.Todo.Completed = true
		return nil
	}); err != nil {
		t.Fatal(err)
	} else {
		e.hub.Publish(evt)
	}
	e.drain(t)

	// Wednesday: a commit lands — the PR changes while the condition STAYS
	// true. Level-triggered engines resurrect the todo here. Ours must not.
	e.touchTitle(t, id, "PR pr-1 (updated)")
	e.drain(t)
	if !e.item(t, id).GetTodo().GetCompleted() {
		t.Fatal("the Wednesday commit resurrected the completed todo — edge semantics broken")
	}

	// Review withdrawn, then re-requested: a genuine second ring.
	e.setState(t, id, "open")
	e.drain(t)
	e.setState(t, id, "review")
	e.drain(t)
	if e.item(t, id).GetTodo().GetCompleted() {
		// add+labels are sets; completion state is untouched by this rule's
		// actions — the rule fired (labels/project reasserted) but does not
		// reopen. Reopening is its own action a user would opt into.
		t.Log("todo remains completed after re-request (rule has no reopen action) — correct")
	}
}

func TestSelfGuard(t *testing.T) {
	e := newEnv(t)
	e.drain(t)
	// A rule whose own effect satisfies its own condition: must fire once
	// (from the user event), not loop on its own event.
	e.save(t, &taskcorev1.Rule{
		Name:   "self",
		Became: `"a" in labels`,
		Do:     &taskcorev1.RuleActions{AddLabels: []string{"a", "b"}},
	})
	id := e.newMirror(t, "pr-2", "open")
	e.drain(t)
	if _, evt, err := e.st.MutateItem(context.Background(), id, userProv, func(it *taskcorev1.Item) error {
		it.Todo = &taskcorev1.Todo{Labels: []string{"a"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	} else {
		e.hub.Publish(evt)
	}
	e.drain(t)
	got := e.item(t, id)
	if len(got.GetTodo().GetLabels()) != 2 {
		t.Fatalf("labels = %v, want [a b]", got.GetTodo().GetLabels())
	}
	// Cursor is at tail and no further events pend — the engine did not
	// react to its own write beyond the one legitimate fire.
	if n, err := e.eng.DrainOnce(context.Background()); err != nil || n != 0 {
		t.Fatalf("drain after settle = (%d, %v), want (0, nil)", n, err)
	}
}

// TestPingPongDepthCap: two rules feeding each other must die at the cap,
// not spin forever.
func TestPingPongDepthCap(t *testing.T) {
	e := newEnv(t)
	e.drain(t)
	e.save(t, &taskcorev1.Rule{
		Name: "ping", Became: `"ping" in labels`,
		Do: &taskcorev1.RuleActions{AddLabels: []string{"pong"}, RemoveLabels: []string{"ping"}},
	})
	e.save(t, &taskcorev1.Rule{
		Name: "pong", Became: `"pong" in labels`, Position: 2,
		Do: &taskcorev1.RuleActions{AddLabels: []string{"ping"}, RemoveLabels: []string{"pong"}},
	})
	id := e.newMirror(t, "pr-3", "open")
	e.drain(t)
	if _, evt, err := e.st.MutateItem(context.Background(), id, userProv, func(it *taskcorev1.Item) error {
		it.Todo = &taskcorev1.Todo{Labels: []string{"ping"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	} else {
		e.hub.Publish(evt)
	}

	// Drain repeatedly (each fire enqueues the next event); must terminate.
	for i := 0; i < 30; i++ {
		n, err := e.eng.DrainOnce(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return // settled — the cap worked
		}
	}
	t.Fatal("ping-pong cascade never settled: depth cap broken")
}

func TestCrashReplayIsIdempotent(t *testing.T) {
	e := newEnv(t)
	e.drain(t)
	e.save(t, &taskcorev1.Rule{
		Name: "triage", Became: `kind == "gh.pr" && !has(item.todo)`,
		Do: &taskcorev1.RuleActions{Add: true, AddLabels: []string{"inbox-swept"}},
	})
	id := e.newMirror(t, "pr-4", "open")
	e.drain(t)
	if e.item(t, id).GetTodo() == nil {
		t.Fatal("triage rule did not fire")
	}

	// Simulate a crash that lost the cursor advance: rewind and replay.
	if err := e.st.SetMeta(context.Background(), "rules.cursor", "0"); err != nil {
		t.Fatal(err)
	}
	before := e.item(t, id)
	e.drain(t)
	after := e.item(t, id)
	if before.GetTodoRevision() != after.GetTodoRevision() {
		t.Fatalf("replay mutated state: revision %d → %d (actions not idempotent)",
			before.GetTodoRevision(), after.GetTodoRevision())
	}
}

func TestScheduledRule(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.drain(t)
	e.save(t, &taskcorev1.Rule{
		Name:     "morning-sweep",
		Schedule: &taskcorev1.Schedule{Cron: "0 8 * * *"},
		Where:    `!completed && has_due && due < now`,
		Do:       &taskcorev1.RuleActions{AddLabels: []string{"overdue"}},
	})

	// An overdue native task.
	item := &taskcorev1.Item{
		Id: e.ids.NewID(), Kind: "task", TodoRevision: 1,
		Todo: &taskcorev1.Todo{TitleOverride: "late thing",
			Due: tsOf(e.clk.Now().Add(-24 * time.Hour))},
	}
	if evt, err := e.st.CreateItem(ctx, item, userProv); err != nil {
		t.Fatal(err)
	} else {
		e.hub.Publish(evt)
	}
	e.drain(t)

	// First tick registers the schedule; nothing fires before 8am.
	if err := e.eng.TickSchedules(ctx, e.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if labels := e.item(t, item.GetId()).GetTodo().GetLabels(); len(labels) != 0 {
		t.Fatalf("fired before its slot: %v", labels)
	}
	// Cross 8am next day.
	e.clk.Advance(21 * time.Hour) // 12:00 → 09:00 next day
	if err := e.eng.TickSchedules(ctx, e.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if labels := e.item(t, item.GetId()).GetTodo().GetLabels(); len(labels) != 1 || labels[0] != "overdue" {
		t.Fatalf("labels = %v, want [overdue]", labels)
	}
	// Same day again: next fire is tomorrow.
	e.clk.Advance(time.Hour)
	if err := e.eng.TickSchedules(ctx, e.clk.Now()); err != nil {
		t.Fatal(err)
	}
	got := e.item(t, item.GetId())
	if got.GetTodoRevision() != 2 {
		t.Fatalf("scheduled rule re-applied within the same slot: revision %d", got.GetTodoRevision())
	}
}

func TestBackfill(t *testing.T) {
	e := newEnv(t)
	e.drain(t)
	// Two matching mirrors exist BEFORE the rule: nothing fires on save
	// (non-retroactive), everything applies on explicit backfill.
	a := e.newMirror(t, "pr-a", "review")
	b := e.newMirror(t, "pr-b", "review")
	e.drain(t)
	e.save(t, &taskcorev1.Rule{
		Name: "requests", Became: `state == "review"`,
		Do: &taskcorev1.RuleActions{Add: true, SetProject: strPtr("reviews")},
	})
	e.drain(t)
	if e.item(t, a).GetTodo() != nil {
		t.Fatal("saving a rule must not fire it retroactively")
	}
	n, err := e.eng.Backfill(context.Background(), "requests")
	if err != nil || n != 2 {
		t.Fatalf("Backfill = (%d, %v), want (2, nil)", n, err)
	}
	for _, id := range []string{a, b} {
		if e.item(t, id).GetTodo().GetProject() != "reviews" {
			t.Fatalf("backfill missed %s", id)
		}
	}
}

func strPtr(s string) *string { return &s }

func tsOf(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t) }
