package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/clock"
	"todoapp/internal/feed"
	"todoapp/internal/query"
	"todoapp/internal/rules"
	"todoapp/internal/store"
)

// newRuleSvc wires a ruleService over the real store, query engine, and
// rules engine (backfill), so these tests exercise the same stack the
// daemon runs.
func newRuleSvc(t *testing.T) (*ruleService, store.Store, clock.IDGen) {
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
	eng := rules.NewEngine(st, hub, qe, clk, log)
	srv := New(st, hub, qe, clk, clock.NewIDGen(clk, 1), nil, eng.Backfill)
	return &ruleService{s: srv}, st, clock.NewIDGen(clk, 2)
}

func mkItem(t *testing.T, st store.Store, ids clock.IDGen, title string, labels ...string) string {
	t.Helper()
	it := &taskcorev1.Item{
		Id:   ids.NewID(),
		Kind: NativeKind,
		Todo: &taskcorev1.Todo{TitleOverride: title, Labels: labels},

		TodoRevision: 1,
	}
	prov := &taskcorev1.Provenance{Source: taskcorev1.ProvenanceSource_PROVENANCE_SOURCE_CLIENT, Ref: "test"}
	if _, err := st.CreateItem(context.Background(), it, prov); err != nil {
		t.Fatal(err)
	}
	return it.GetId()
}

func becameRule(name, cond string) *taskcorev1.Rule {
	return &taskcorev1.Rule{
		Name:   name,
		Became: cond,
		Do:     &taskcorev1.RuleActions{AddLabels: []string{"hit-" + name}},
	}
}

func wantCode(t *testing.T, err error, code codes.Code) {
	t.Helper()
	if status.Code(err) != code {
		t.Fatalf("status = %v (%v), want %v", status.Code(err), err, code)
	}
}

func TestSaveRuleValidation(t *testing.T) {
	svc, _, _ := newRuleSvc(t)
	ctx := context.Background()

	// A rule that would not compile must not save.
	_, err := svc.SaveRule(ctx, &taskcorev1.SaveRuleRequest{Rule: becameRule("bad", "nonexistent_field == 1")})
	wantCode(t, err, codes.InvalidArgument)

	// Exactly one trigger.
	both := becameRule("both", "completed")
	both.Schedule = &taskcorev1.Schedule{Cron: "0 8 * * *"}
	_, err = svc.SaveRule(ctx, &taskcorev1.SaveRuleRequest{Rule: both})
	wantCode(t, err, codes.InvalidArgument)

	// Actions are required.
	_, err = svc.SaveRule(ctx, &taskcorev1.SaveRuleRequest{Rule: &taskcorev1.Rule{Name: "noop", Became: "completed"}})
	wantCode(t, err, codes.InvalidArgument)

	if list, err := svc.ListRules(ctx, &taskcorev1.ListRulesRequest{}); err != nil || len(list.GetRules()) != 0 {
		t.Fatalf("rejected rules must not persist: %v, %v", list.GetRules(), err)
	}
}

func TestSaveRulePositions(t *testing.T) {
	svc, _, _ := newRuleSvc(t)
	ctx := context.Background()

	r1, err := svc.SaveRule(ctx, &taskcorev1.SaveRuleRequest{Rule: becameRule("r1", "completed")})
	if err != nil {
		t.Fatal(err)
	}
	if r1.GetRule().GetPosition() != 1 {
		t.Fatalf("first rule position = %d, want 1 (response must carry the resolved position)", r1.GetRule().GetPosition())
	}
	r2, err := svc.SaveRule(ctx, &taskcorev1.SaveRuleRequest{Rule: becameRule("r2", "completed")})
	if err != nil {
		t.Fatal(err)
	}
	if r2.GetRule().GetPosition() != 2 {
		t.Fatalf("second rule position = %d, want 2", r2.GetRule().GetPosition())
	}

	// Upsert carrying its position keeps its evaluation slot.
	upd := becameRule("r1", "!completed")
	upd.Position = r1.GetRule().GetPosition()
	saved, err := svc.SaveRule(ctx, &taskcorev1.SaveRuleRequest{Rule: upd})
	if err != nil {
		t.Fatal(err)
	}
	if saved.GetRule().GetPosition() != 1 {
		t.Fatalf("upsert moved r1 to position %d, want 1", saved.GetRule().GetPosition())
	}
	list, err := svc.ListRules(ctx, &taskcorev1.ListRulesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.GetRules()) != 2 || list.GetRules()[0].GetName() != "r1" || list.GetRules()[1].GetName() != "r2" {
		t.Fatalf("evaluation order after upsert: %v", list.GetRules())
	}
	if list.GetRules()[0].GetBecame() != "!completed" {
		t.Fatalf("upsert did not replace the rule body: %q", list.GetRules()[0].GetBecame())
	}
}

func TestRuleCRUDErrors(t *testing.T) {
	svc, _, _ := newRuleSvc(t)
	ctx := context.Background()

	_, err := svc.GetRule(ctx, &taskcorev1.GetRuleRequest{Name: "ghost"})
	wantCode(t, err, codes.NotFound)
	_, err = svc.DeleteRule(ctx, &taskcorev1.DeleteRuleRequest{Name: "ghost"})
	wantCode(t, err, codes.NotFound)

	if _, err := svc.SaveRule(ctx, &taskcorev1.SaveRuleRequest{Rule: becameRule("real", "completed")}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DeleteRule(ctx, &taskcorev1.DeleteRuleRequest{Name: "real"}); err != nil {
		t.Fatal(err)
	}
	_, err = svc.GetRule(ctx, &taskcorev1.GetRuleRequest{Name: "real"})
	wantCode(t, err, codes.NotFound)
}

func TestDryRunLevels(t *testing.T) {
	svc, st, ids := newRuleSvc(t)
	ctx := context.Background()

	mkItem(t, st, ids, "one", "pending")
	mkItem(t, st, ids, "two", "pending")
	mkItem(t, st, ids, "three")

	// A broken rule must not scan.
	_, err := svc.DryRunRule(ctx, &taskcorev1.DryRunRuleRequest{Rule: becameRule("bad", "nope ===")})
	wantCode(t, err, codes.InvalidArgument)

	// became evaluated as a level over current items; works unsaved.
	resp, err := svc.DryRunRule(ctx, &taskcorev1.DryRunRuleRequest{Rule: becameRule("sweep", `"pending" in labels`)})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetTotalMatches() != 2 || len(resp.GetMatches()) != 2 {
		t.Fatalf("dryrun became: total=%d matches=%d, want 2/2", resp.GetTotalMatches(), len(resp.GetMatches()))
	}

	// Schedule rules dry-run their `where`.
	sched := &taskcorev1.Rule{
		Name:     "sweep-cron",
		Schedule: &taskcorev1.Schedule{Cron: "0 8 * * *"},
		Where:    "!completed",
		Do:       &taskcorev1.RuleActions{AddLabels: []string{"overdue"}},
	}
	resp, err = svc.DryRunRule(ctx, &taskcorev1.DryRunRuleRequest{Rule: sched})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetTotalMatches() != 3 {
		t.Fatalf("dryrun where: total=%d, want 3", resp.GetTotalMatches())
	}
}

func TestDryRunCap(t *testing.T) {
	svc, st, ids := newRuleSvc(t)
	ctx := context.Background()
	for i := 0; i < dryRunMatchCap+5; i++ {
		mkItem(t, st, ids, fmt.Sprintf("bulk %d", i), "bulk")
	}
	resp, err := svc.DryRunRule(ctx, &taskcorev1.DryRunRuleRequest{Rule: becameRule("bulk", `"bulk" in labels`)})
	if err != nil {
		t.Fatal(err)
	}
	if got := int(resp.GetTotalMatches()); got != dryRunMatchCap+5 {
		t.Fatalf("total_matches = %d, want %d (must count past the cap)", got, dryRunMatchCap+5)
	}
	if len(resp.GetMatches()) != dryRunMatchCap {
		t.Fatalf("matches = %d, want capped at %d", len(resp.GetMatches()), dryRunMatchCap)
	}
}

func TestBackfillRule(t *testing.T) {
	svc, st, ids := newRuleSvc(t)
	ctx := context.Background()

	_, err := svc.BackfillRule(ctx, &taskcorev1.BackfillRuleRequest{Name: "ghost"})
	wantCode(t, err, codes.NotFound)

	a := mkItem(t, st, ids, "one", "pending")
	b := mkItem(t, st, ids, "two", "pending")
	mkItem(t, st, ids, "three")
	rule := &taskcorev1.Rule{
		Name:   "sweep",
		Became: `"pending" in labels`,
		Do:     &taskcorev1.RuleActions{AddLabels: []string{"swept"}},
	}
	if _, err := svc.SaveRule(ctx, &taskcorev1.SaveRuleRequest{Rule: rule}); err != nil {
		t.Fatal(err)
	}
	resp, err := svc.BackfillRule(ctx, &taskcorev1.BackfillRuleRequest{Name: "sweep"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetApplied() != 2 {
		t.Fatalf("applied = %d, want 2", resp.GetApplied())
	}
	for _, id := range []string{a, b} {
		it, err := st.GetItem(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got := it.GetTodo().GetLabels(); len(got) != 2 || got[1] != "swept" {
			t.Fatalf("labels after backfill = %v", got)
		}
	}
	// Idempotent: everything already in the desired state applies to nothing.
	resp, err = svc.BackfillRule(ctx, &taskcorev1.BackfillRuleRequest{Name: "sweep"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetApplied() != 0 {
		t.Fatalf("second backfill applied = %d, want 0", resp.GetApplied())
	}
}

func TestListRuleTemplates(t *testing.T) {
	svc, st, _ := newRuleSvc(t)
	ctx := context.Background()

	// No manifests: empty, not an error.
	resp, err := svc.ListRuleTemplates(ctx, &taskcorev1.ListRuleTemplatesRequest{})
	if err != nil || len(resp.GetTemplates()) != 0 {
		t.Fatalf("empty templates: %v, %v", resp.GetTemplates(), err)
	}

	save := func(plugin string, ruleNames ...string) {
		m := &pluginv1.Manifest{Name: plugin}
		for _, n := range ruleNames {
			m.RuleTemplates = append(m.RuleTemplates, becameRule(n, "completed"))
		}
		if err := st.SaveManifest(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	save("zeta", "z-second", "a-first")
	save("alpha", "only")

	resp, err = svc.ListRuleTemplates(ctx, &taskcorev1.ListRuleTemplatesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tm := range resp.GetTemplates() {
		got = append(got, tm.GetPlugin()+"/"+tm.GetRule().GetName())
	}
	want := []string{"alpha/only", "zeta/a-first", "zeta/z-second"}
	if len(got) != len(want) {
		t.Fatalf("templates = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("templates = %v, want %v (deterministic plugin,name order)", got, want)
		}
	}
}
