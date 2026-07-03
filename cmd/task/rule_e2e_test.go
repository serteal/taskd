package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

// NOTE: short test names on purpose — t.TempDir() feeds the unix socket
// path (macOS caps it at 104 bytes).
//
// The daemon runs the real rules engine. It drains the feed event-driven,
// so effects usually land in milliseconds; waitTrue polls generously for
// -race builds. Where a test must prove something did NOT happen, it uses a
// canary item instead of a sleep: the engine processes events in order, so
// once a later event's effect is visible, all earlier events are done.

// waitTrue polls cond for up to ~2.5s and reports whether it became true.
func waitTrue(cond func() bool) bool {
	deadline := time.Now().Add(2500 * time.Millisecond)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// writeYAML drops a rules file into a temp dir and returns its path.
func writeYAML(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rules.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// itemJSON fetches one item via `--json show`.
func itemJSON(t *testing.T, dir, id string) *taskcorev1.Item {
	t.Helper()
	out := mustRun(t, dir, "--json", "show", id)
	it := &taskcorev1.Item{}
	if err := protojson.Unmarshal([]byte(strings.TrimSpace(out)), it); err != nil {
		t.Fatalf("show --json %q: %v", out, err)
	}
	return it
}

func hasLabel(it *taskcorev1.Item, label string) bool {
	return slices.Contains(it.GetTodo().GetLabels(), label)
}

// TestRuleFire: an applied became-rule fires on CREATE, and an edit that
// keeps the condition true does not re-fire it (no duplicate effects).
func TestRuleFire(t *testing.T) {
	dir := startDaemon(t)
	f := writeYAML(t, `
rules:
  - name: review-requests
    became: '"hot" in labels'
    do: { project: fire }
`)
	if out := mustRun(t, dir, "rule", "apply", "-f", f); !strings.Contains(out, "saved review-requests") {
		t.Fatalf("apply output %q", out)
	}

	id := addItem(t, dir, "hot pr", "-l", "hot")
	if !waitTrue(func() bool { return strings.Contains(mustRun(t, dir, "ls", "-p", "fire"), id) }) {
		t.Fatalf("rule never set project=fire on the new item:\n%s", mustRun(t, dir, "ls", "--all"))
	}
	rev := itemJSON(t, dir, id).GetTodoRevision()

	// Touch the note: the condition stays true — level, not edge.
	mustRun(t, dir, "edit", id, "--note", "touched")

	// Canary barrier: once a LATER matching create has fired, the engine
	// has processed the edit above too.
	canary := addItem(t, dir, "canary", "-l", "hot")
	if !waitTrue(func() bool { return strings.Contains(mustRun(t, dir, "ls", "-p", "fire"), canary) }) {
		t.Fatal("canary item never got project=fire")
	}

	it := itemJSON(t, dir, id)
	if got := it.GetTodo().GetProject(); got != "fire" {
		t.Fatalf("project after note edit = %q, want fire", got)
	}
	if got := it.GetTodoRevision(); got != rev+1 {
		t.Fatalf("todo_revision = %d, want %d (the note edit only; a re-fire would add writes)", got, rev+1)
	}
}

// TestRuleEdge: the doorbell, end to end. Removing the rule's effect while
// the condition stays true must not re-apply it; a genuine false→true flip
// must.
func TestRuleEdge(t *testing.T) {
	dir := startDaemon(t)
	f := writeYAML(t, `
rules:
  - name: edge
    became: '"urgent" in labels'
    do: { labels: { add: [flagged] } }
`)
	mustRun(t, dir, "rule", "apply", "-f", f)

	id := addItem(t, dir, "thing")
	mustRun(t, dir, "edit", id, "--add-label", "urgent")
	if !waitTrue(func() bool { return hasLabel(itemJSON(t, dir, id), "flagged") }) {
		t.Fatal("edge rule never fired on the false→true flip")
	}

	// Undo the effect; the condition (urgent) is still true, so no edge.
	mustRun(t, dir, "edit", id, "--rm-label", "flagged")
	canary := addItem(t, dir, "c", "-l", "urgent")
	if !waitTrue(func() bool { return hasLabel(itemJSON(t, dir, canary), "flagged") }) {
		t.Fatal("canary create never got flagged")
	}
	if hasLabel(itemJSON(t, dir, id), "flagged") {
		t.Fatal("rule re-fired while the condition stayed true (level semantics leaked in)")
	}

	// Genuine new edge: condition goes false, then true again.
	mustRun(t, dir, "edit", id, "--rm-label", "urgent")
	mustRun(t, dir, "edit", id, "--add-label", "urgent")
	if !waitTrue(func() bool { return hasLabel(itemJSON(t, dir, id), "flagged") }) {
		t.Fatal("a genuine false→true flip must re-fire the rule")
	}
}

// TestRuleValid: apply rejects rules the engine could never run, naming the
// problem, and persists nothing.
func TestRuleValid(t *testing.T) {
	dir := startDaemon(t)

	bad := writeYAML(t, `
rules:
  - name: broken
    became: 'nonexistent_field == 1'
    do: { add: true }
`)
	_, _, err := run(t, dir, "rule", "apply", "-f", bad)
	if err == nil {
		t.Fatal("apply of a bad CEL expression must fail")
	}
	if !strings.Contains(err.Error(), "nonexistent_field") {
		t.Fatalf("error %q must mention the offending expression", err)
	}

	both := writeYAML(t, `
rules:
  - name: two-triggers
    became: 'completed'
    schedule: "0 8 * * *"
    where: 'completed'
    do: { add: true }
`)
	_, _, err = run(t, dir, "rule", "apply", "-f", both)
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("became+schedule must be rejected, got %v", err)
	}

	typo := writeYAML(t, `
rules:
  - name: typo
    becam: 'completed'
    do: { add: true }
`)
	_, _, err = run(t, dir, "rule", "apply", "-f", typo)
	if err == nil || !strings.Contains(err.Error(), "becam") {
		t.Fatalf("unknown YAML keys must error, not silently no-op; got %v", err)
	}

	if out := mustRun(t, dir, "rule", "ls"); rows(out) != 0 {
		t.Fatalf("no rule may survive a failed apply:\n%s", out)
	}
}

// TestRuleRT: ls/show render, and export→apply is a fixed point.
func TestRuleRT(t *testing.T) {
	dir := startDaemon(t)
	f := writeYAML(t, `
rules:
  - name: r1
    description: first responder
    became: '"a" in labels'
    do: { add: true, project: reviews, labels: { add: [code] } }
  - name: r2
    schedule: "0 8 * * *"
    where: '!completed'
    do: { labels: { add: [overdue] }, due_in: "24h" }
`)
	out := mustRun(t, dir, "rule", "apply", "-f", f)
	for _, want := range []string{"saved r1", "saved r2"} {
		if !strings.Contains(out, want) {
			t.Fatalf("apply output missing %q:\n%s", want, out)
		}
	}

	ls1 := mustRun(t, dir, "rule", "ls")
	for _, want := range []string{
		`became: "a" in labels`, "cron: 0 8 * * *",
		"add, +code, project=reviews", "+overdue",
	} {
		if !strings.Contains(ls1, want) {
			t.Fatalf("rule ls missing %q:\n%s", want, ls1)
		}
	}
	show := mustRun(t, dir, "rule", "show", "r1")
	for _, want := range []string{"first responder", "position:", "1"} {
		if !strings.Contains(show, want) {
			t.Fatalf("rule show missing %q:\n%s", want, show)
		}
	}

	before := rulesJSON(t, dir)
	exported := filepath.Join(t.TempDir(), "exported.yaml")
	mustRun(t, dir, "rule", "export", "-o", exported)
	mustRun(t, dir, "rule", "apply", "-f", exported)

	if ls2 := mustRun(t, dir, "rule", "ls"); ls2 != ls1 {
		t.Fatalf("export→apply changed rule ls:\nbefore:\n%s\nafter:\n%s", ls1, ls2)
	}
	after := rulesJSON(t, dir)
	if len(before) != len(after) {
		t.Fatalf("rule count changed: %d → %d", len(before), len(after))
	}
	for i := range before {
		if !proto.Equal(before[i], after[i]) {
			t.Fatalf("rule %s changed across export→apply:\nbefore: %v\nafter:  %v",
				before[i].GetName(), before[i], after[i])
		}
	}
}

// rulesJSON returns all rules via `--json rule ls`, parsed.
func rulesJSON(t *testing.T, dir string) []*taskcorev1.Rule {
	t.Helper()
	out := strings.TrimSpace(mustRun(t, dir, "--json", "rule", "ls"))
	if out == "" {
		return nil
	}
	var rules []*taskcorev1.Rule
	for _, line := range strings.Split(out, "\n") {
		r := &taskcorev1.Rule{}
		if err := protojson.Unmarshal([]byte(line), r); err != nil {
			t.Fatalf("rule ls --json line %q: %v", line, err)
		}
		rules = append(rules, r)
	}
	return rules
}

// TestRuleDryBF: dryrun previews without writing; backfill is the explicit
// retroactive apply.
func TestRuleDryBF(t *testing.T) {
	dir := startDaemon(t)
	i1 := addItem(t, dir, "one", "-l", "pending")
	i2 := addItem(t, dir, "two", "-l", "pending")

	// Canary barrier BEFORE the sweep rule exists: once the canary's create
	// has fired, the engine's cursor is past the two creates above, so
	// saving the sweep rule cannot retroactively catch them.
	fb := writeYAML(t, `
rules:
  - name: barrier
    became: '"c" in labels'
    do: { labels: { add: [seen] } }
`)
	mustRun(t, dir, "rule", "apply", "-f", fb)
	c := addItem(t, dir, "c item", "-l", "c")
	if !waitTrue(func() bool { return hasLabel(itemJSON(t, dir, c), "seen") }) {
		t.Fatal("barrier canary never fired")
	}

	fs := writeYAML(t, `
rules:
  - name: sweep
    became: '"pending" in labels'
    do: { labels: { add: [swept] } }
`)
	mustRun(t, dir, "rule", "apply", "-f", fs)

	out := mustRun(t, dir, "rule", "dryrun", "sweep")
	if !strings.Contains(out, "2 matching") {
		t.Fatalf("dryrun total: %q", out)
	}
	for _, want := range []string{"one", "two"} {
		if !strings.Contains(out, want) {
			t.Fatalf("dryrun missing match line for %q:\n%s", want, out)
		}
	}
	if hasLabel(itemJSON(t, dir, i1), "swept") || hasLabel(itemJSON(t, dir, i2), "swept") {
		t.Fatal("dryrun must not write")
	}

	out = mustRun(t, dir, "rule", "backfill", "sweep")
	if !strings.Contains(out, "applied to 2 items") {
		t.Fatalf("backfill output %q, want applied to 2 items", out)
	}
	if !hasLabel(itemJSON(t, dir, i1), "swept") || !hasLabel(itemJSON(t, dir, i2), "swept") {
		t.Fatal("backfill must apply the rule's actions")
	}
}

// TestRuleOff: a disabled rule does not fire on a fresh matching add;
// enable restores it; rm removes it.
func TestRuleOff(t *testing.T) {
	dir := startDaemon(t)
	f := writeYAML(t, `
rules:
  - name: main
    became: '"z" in labels'
    do: { labels: { add: [zz] } }
  - name: canary
    became: '"z" in labels'
    do: { labels: { add: [seen] } }
`)
	mustRun(t, dir, "rule", "apply", "-f", f)
	mustRun(t, dir, "rule", "disable", "main")

	ls := mustRun(t, dir, "rule", "ls")
	for _, line := range strings.Split(ls, "\n") {
		if strings.HasPrefix(line, "main") && !strings.Contains(line, "yes") {
			t.Fatalf("rule ls must mark main disabled:\n%s", ls)
		}
	}

	z1 := addItem(t, dir, "z one", "-l", "z")
	if !waitTrue(func() bool { return hasLabel(itemJSON(t, dir, z1), "seen") }) {
		t.Fatal("enabled canary rule never fired")
	}
	if hasLabel(itemJSON(t, dir, z1), "zz") {
		t.Fatal("disabled rule fired")
	}

	mustRun(t, dir, "rule", "enable", "main")
	z2 := addItem(t, dir, "z two", "-l", "z")
	if !waitTrue(func() bool { return hasLabel(itemJSON(t, dir, z2), "zz") }) {
		t.Fatal("re-enabled rule must fire on a fresh matching add")
	}

	mustRun(t, dir, "rule", "rm", "canary")
	if out := mustRun(t, dir, "rule", "ls"); strings.Contains(out, "canary") {
		t.Fatalf("removed rule still listed:\n%s", out)
	}
	if _, _, err := run(t, dir, "rule", "show", "canary"); err == nil {
		t.Fatal("show of a removed rule must fail")
	}
	if _, _, err := run(t, dir, "rule", "rm", "canary"); err == nil {
		t.Fatal("rm of a missing rule must fail")
	}
}
