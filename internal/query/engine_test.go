package query

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/store"
)

// fixedNow is the fake clock instant used by every test.
var fixedNow = time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := NewEngine(func() time.Time { return fixedNow })
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

func ts(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t) }

func TestMatching(t *testing.T) {
	e := newTestEngine(t)

	dueEarly := fixedNow.AddDate(0, -8, 0)  // 2025-11-03, before 2026-01-01
	dueLate := fixedNow.AddDate(0, 2, 0)    // 2026-09-03, after 2026-01-01
	snoozeFuture := fixedNow.Add(time.Hour) // still snoozed
	snoozePast := fixedNow.Add(-time.Hour)  // expired

	tests := []struct {
		name   string
		filter string
		item   *taskcorev1.Item
		want   bool
	}{
		{
			name:   "label membership hit",
			filter: `"urgent" in labels`,
			item:   &taskcorev1.Item{Kind: "task", Todo: &taskcorev1.Todo{Labels: []string{"urgent", "x"}}},
			want:   true,
		},
		{
			name:   "label membership miss",
			filter: `"urgent" in labels`,
			item:   &taskcorev1.Item{Kind: "task", Todo: &taskcorev1.Todo{Labels: []string{"x"}}},
			want:   false,
		},
		{
			name:   "label membership on item without todo",
			filter: `"urgent" in labels`,
			item:   &taskcorev1.Item{Kind: "github.pr", Mirror: &taskcorev1.Mirror{Title: "PR"}},
			want:   false,
		},
		{
			name:   "due before cutoff",
			filter: `has_due && due < timestamp("2026-01-01T00:00:00Z")`,
			item:   &taskcorev1.Item{Kind: "task", Todo: &taskcorev1.Todo{Due: ts(dueEarly)}},
			want:   true,
		},
		{
			name:   "due after cutoff",
			filter: `has_due && due < timestamp("2026-01-01T00:00:00Z")`,
			item:   &taskcorev1.Item{Kind: "task", Todo: &taskcorev1.Todo{Due: ts(dueLate)}},
			want:   false,
		},
		{
			name:   "no due at all",
			filter: `has_due && due < timestamp("2026-01-01T00:00:00Z")`,
			item:   &taskcorev1.Item{Kind: "task", Todo: &taskcorev1.Todo{}},
			want:   false,
		},
		{
			name:   "snoozed strictly in the future",
			filter: `snoozed`,
			item:   &taskcorev1.Item{Kind: "task", Todo: &taskcorev1.Todo{SnoozedUntil: ts(snoozeFuture)}},
			want:   true,
		},
		{
			name:   "snoozed_until exactly now is not snoozed",
			filter: `snoozed`,
			item:   &taskcorev1.Item{Kind: "task", Todo: &taskcorev1.Todo{SnoozedUntil: ts(fixedNow)}},
			want:   false,
		},
		{
			name:   "snooze expired",
			filter: `snoozed`,
			item:   &taskcorev1.Item{Kind: "task", Todo: &taskcorev1.Todo{SnoozedUntil: ts(snoozePast)}},
			want:   false,
		},
		{
			name:   "snoozed_until absent",
			filter: `snoozed`,
			item:   &taskcorev1.Item{Kind: "task", Todo: &taskcorev1.Todo{}},
			want:   false,
		},
		{
			name:   "title uses override when set",
			filter: `title == "my title"`,
			item: &taskcorev1.Item{Kind: "github.pr",
				Mirror: &taskcorev1.Mirror{Title: "remote title"},
				Todo:   &taskcorev1.Todo{TitleOverride: "my title"}},
			want: true,
		},
		{
			name:   "title falls back to mirror title",
			filter: `title == "remote title"`,
			item: &taskcorev1.Item{Kind: "github.pr",
				Mirror: &taskcorev1.Mirror{Title: "remote title"},
				Todo:   &taskcorev1.Todo{}},
			want: true,
		},
		{
			name:   "deep item access on note",
			filter: `item.todo.note == "remember the milk"`,
			item:   &taskcorev1.Item{Kind: "task", Todo: &taskcorev1.Todo{Note: "remember the milk"}},
			want:   true,
		},
		{
			name:   "has() on absent mirror",
			filter: `has(item.mirror)`,
			item:   &taskcorev1.Item{Kind: "task", Todo: &taskcorev1.Todo{}},
			want:   false,
		},
		{
			name:   "state on a mirrored item",
			filter: `state == "merged"`,
			item:   &taskcorev1.Item{Kind: "github.pr", Mirror: &taskcorev1.Mirror{State: "merged"}},
			want:   true,
		},
		{
			name:   "state empty when mirror absent",
			filter: `state == ""`,
			item:   &taskcorev1.Item{Kind: "task", Todo: &taskcorev1.Todo{}},
			want:   true,
		},
		{
			name:   "completed is false on todo-less item",
			filter: `completed`,
			item:   &taskcorev1.Item{Kind: "github.pr", Mirror: &taskcorev1.Mirror{}},
			want:   false,
		},
		{
			name:   "not completed matches todo-less item",
			filter: `!completed`,
			item:   &taskcorev1.Item{Kind: "github.pr", Mirror: &taskcorev1.Mirror{}},
			want:   true,
		},
		{
			name:   "now variable comes from the injected clock",
			filter: `now == timestamp("2026-07-03T12:00:00Z")`,
			item:   &taskcorev1.Item{Kind: "task", Todo: &taskcorev1.Todo{}},
			want:   true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			match, err := e.Matcher(tc.filter)
			if err != nil {
				t.Fatalf("Matcher(%q): %v", tc.filter, err)
			}
			got, err := match(tc.item)
			if err != nil {
				t.Fatalf("match: %v", err)
			}
			if got != tc.want {
				t.Errorf("filter %q = %v, want %v", tc.filter, got, tc.want)
			}
		})
	}
}

func TestPushdown(t *testing.T) {
	e := newTestEngine(t)

	openWithX := &taskcorev1.Item{Kind: "task", Todo: &taskcorev1.Todo{Labels: []string{"x"}}}
	doneWithX := &taskcorev1.Item{Kind: "task", Todo: &taskcorev1.Todo{Completed: true, Labels: []string{"x"}}}
	openNoX := &taskcorev1.Item{Kind: "task", Todo: &taskcorev1.Todo{}}
	projA := &taskcorev1.Item{Kind: "task", Todo: &taskcorev1.Todo{Project: "a"}}

	tests := []struct {
		name         string
		filter       string
		wantWhere    string
		wantArgs     []any
		wantResidual bool
		// residual expectations, checked only when wantResidual
		residualCases map[*taskcorev1.Item]bool
	}{
		{
			name:      "negated completed fully pushed",
			filter:    `!completed`,
			wantWhere: "completed = 0",
		},
		{
			name:      "completed fully pushed",
			filter:    `completed`,
			wantWhere: "completed = 1",
		},
		{
			name:      "conjunction of pushable conjuncts",
			filter:    `!completed && kind == "task"`,
			wantWhere: "completed = 0 AND kind = ?",
			wantArgs:  []any{"task"},
		},
		{
			name:      "reversed equality operands",
			filter:    `"work" == project`,
			wantWhere: "project = ?",
			wantArgs:  []any{"work"},
		},
		{
			name:         "partial pushdown keeps full residual",
			filter:       `!completed && "x" in labels`,
			wantWhere:    "completed = 0",
			wantResidual: true,
			residualCases: map[*taskcorev1.Item]bool{
				openWithX: true,
				doneWithX: false, // residual re-checks the pushed conjunct too
				openNoX:   false,
			},
		},
		{
			name:         "top-level || gets no pushdown",
			filter:       `completed || project == "a"`,
			wantWhere:    "",
			wantResidual: true,
			residualCases: map[*taskcorev1.Item]bool{
				doneWithX: true,
				projA:     true,
				openNoX:   false,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := e.Compile(tc.filter)
			if err != nil {
				t.Fatalf("Compile(%q): %v", tc.filter, err)
			}
			if c.Where != tc.wantWhere {
				t.Errorf("Where = %q, want %q", c.Where, tc.wantWhere)
			}
			if len(c.Args) != len(tc.wantArgs) {
				t.Fatalf("Args = %v, want %v", c.Args, tc.wantArgs)
			}
			for i := range tc.wantArgs {
				if c.Args[i] != tc.wantArgs[i] {
					t.Errorf("Args[%d] = %v, want %v", i, c.Args[i], tc.wantArgs[i])
				}
			}
			if tc.wantResidual != (c.Residual != nil) {
				t.Fatalf("Residual != nil is %v, want %v", c.Residual != nil, tc.wantResidual)
			}
			for item, want := range tc.residualCases {
				got, err := c.Residual(item)
				if err != nil {
					t.Fatalf("Residual: %v", err)
				}
				if got != want {
					t.Errorf("Residual(%v) = %v, want %v", item, got, want)
				}
			}
		})
	}
}

func TestCompileEmptyFilter(t *testing.T) {
	e := newTestEngine(t)
	for _, filter := range []string{"", "   \t\n"} {
		c, err := e.Compile(filter)
		if err != nil {
			t.Fatalf("Compile(%q): %v", filter, err)
		}
		if c.Where != "" || len(c.Args) != 0 || c.Residual != nil {
			t.Errorf("Compile(%q) = %+v, want match-everything zero value", filter, c)
		}
	}
}

func TestCompileErrors(t *testing.T) {
	e := newTestEngine(t)
	tests := []struct {
		name       string
		filter     string
		wantSubstr string
	}{
		{"unparseable", `completed &&`, "Syntax error"},
		{"type error", `due == 3`, "no matching overload"},
		{"unknown variable", `frobnicate`, "undeclared reference"},
		{"non-boolean result", `title`, "must evaluate to a boolean"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := e.Compile(tc.filter)
			if err == nil {
				t.Fatalf("Compile(%q) succeeded, want error", tc.filter)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("Compile(%q) error = %q, want it to mention %q", tc.filter, err, tc.wantSubstr)
			}
			// Matcher shares the compile path and must fail identically.
			if _, merr := e.Matcher(tc.filter); merr == nil {
				t.Errorf("Matcher(%q) succeeded, want error", tc.filter)
			}
		})
	}
}

func TestMatcher(t *testing.T) {
	e := newTestEngine(t)

	t.Run("empty filter matches everything", func(t *testing.T) {
		match, err := e.Matcher("")
		if err != nil {
			t.Fatalf("Matcher(\"\"): %v", err)
		}
		for _, item := range []*taskcorev1.Item{
			{Kind: "task", Todo: &taskcorev1.Todo{Completed: true}},
			{Kind: "github.pr", Mirror: &taskcorev1.Mirror{}},
		} {
			got, err := match(item)
			if err != nil {
				t.Fatalf("match: %v", err)
			}
			if !got {
				t.Errorf("empty filter did not match %v", item)
			}
		}
	})

	t.Run("fully pushed-down filter still evaluates per item", func(t *testing.T) {
		match, err := e.Matcher(`!completed`)
		if err != nil {
			t.Fatalf("Matcher: %v", err)
		}
		open := &taskcorev1.Item{Kind: "task", Todo: &taskcorev1.Todo{}}
		done := &taskcorev1.Item{Kind: "task", Todo: &taskcorev1.Todo{Completed: true}}
		if got, err := match(open); err != nil || !got {
			t.Errorf("match(open) = (%v, %v), want (true, nil)", got, err)
		}
		if got, err := match(done); err != nil || got {
			t.Errorf("match(done) = (%v, %v), want (false, nil)", got, err)
		}
	})

	t.Run("invalid filter propagates error", func(t *testing.T) {
		_, err := e.Matcher(`nonsense ==`)
		if err == nil {
			t.Fatal("Matcher succeeded, want error")
		}
		if !strings.Contains(err.Error(), "Syntax error") {
			t.Errorf("error = %q, want it to mention the syntax error", err)
		}
	})
}

func TestBindings(t *testing.T) {
	e := newTestEngine(t)

	// A stand-in "due" binding into a mirror timestamp field.
	if err := e.RegisterKind("fake.kind", map[string]string{
		"due": "item.mirror.missing_since",
	}); err != nil {
		t.Fatalf("RegisterKind: %v", err)
	}

	boundTime := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	overrideTime := time.Date(2026, 9, 15, 18, 30, 0, 0, time.UTC)

	t.Run("falls back to binding when todo.due unset", func(t *testing.T) {
		item := &taskcorev1.Item{
			Kind:   "fake.kind",
			Mirror: &taskcorev1.Mirror{MissingSince: ts(boundTime)},
			Todo:   &taskcorev1.Todo{},
		}
		got := e.EffectiveDue(item)
		if got == nil || !got.Equal(boundTime) {
			t.Errorf("EffectiveDue = %v, want %v", got, boundTime)
		}
	})

	t.Run("todo.due override wins", func(t *testing.T) {
		item := &taskcorev1.Item{
			Kind:   "fake.kind",
			Mirror: &taskcorev1.Mirror{MissingSince: ts(boundTime)},
			Todo:   &taskcorev1.Todo{Due: ts(overrideTime)},
		}
		got := e.EffectiveDue(item)
		if got == nil || !got.Equal(overrideTime) {
			t.Errorf("EffectiveDue = %v, want %v", got, overrideTime)
		}
	})

	t.Run("absent bound field means no due", func(t *testing.T) {
		item := &taskcorev1.Item{Kind: "fake.kind", Mirror: &taskcorev1.Mirror{}}
		if got := e.EffectiveDue(item); got != nil {
			t.Errorf("EffectiveDue = %v, want nil", got)
		}
	})

	t.Run("unknown kind with no todo.due means no due", func(t *testing.T) {
		item := &taskcorev1.Item{Kind: "unregistered.kind", Todo: &taskcorev1.Todo{}}
		if got := e.EffectiveDue(item); got != nil {
			t.Errorf("EffectiveDue = %v, want nil", got)
		}
	})

	t.Run("binding that does not type-check errors", func(t *testing.T) {
		err := e.RegisterKind("bad.kind", map[string]string{"due": "item.nonexistent"})
		if err == nil {
			t.Fatal("RegisterKind succeeded, want error")
		}
		if !strings.Contains(err.Error(), "undefined field") {
			t.Errorf("error = %q, want it to mention the undefined field", err)
		}
	})

	t.Run("due binding with a non-timestamp type errors", func(t *testing.T) {
		err := e.RegisterKind("bad.kind2", map[string]string{"due": "item.todo.note"})
		if err == nil {
			t.Fatal("RegisterKind succeeded, want error")
		}
		if !strings.Contains(err.Error(), "timestamp") {
			t.Errorf("error = %q, want it to mention the timestamp requirement", err)
		}
	})

	t.Run("virtual due variable uses the binding in filters", func(t *testing.T) {
		match, err := e.Matcher(`has_due && due == timestamp("2026-08-01T09:00:00Z")`)
		if err != nil {
			t.Fatalf("Matcher: %v", err)
		}
		item := &taskcorev1.Item{
			Kind:   "fake.kind",
			Mirror: &taskcorev1.Mirror{MissingSince: ts(boundTime)},
		}
		got, err := match(item)
		if err != nil {
			t.Fatalf("match: %v", err)
		}
		if !got {
			t.Error("filter over virtual due did not match the bound mirror value")
		}
	})
}

func TestExtract(t *testing.T) {
	e := newTestEngine(t)

	due := time.Date(2026, 7, 10, 8, 0, 0, 0, time.UTC)
	snooze := time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		item *taskcorev1.Item
		want store.Indexed
	}{
		{
			name: "native with due and snooze",
			item: &taskcorev1.Item{Kind: "task", Todo: &taskcorev1.Todo{
				Project:      "work/reviews",
				Due:          ts(due),
				SnoozedUntil: ts(snooze),
			}},
			want: store.Indexed{Completed: false, Project: "work/reviews", Due: &due, SnoozedUntil: &snooze},
		},
		{
			name: "native completed without due or snooze",
			item: &taskcorev1.Item{Kind: "task", Todo: &taskcorev1.Todo{Completed: true, Project: "home"}},
			want: store.Indexed{Completed: true, Project: "home", Due: nil, SnoozedUntil: nil},
		},
		{
			name: "todo-less mirror",
			item: &taskcorev1.Item{Kind: "github.pr", Mirror: &taskcorev1.Mirror{Title: "PR", State: "open"}},
			want: store.Indexed{Completed: false, Project: "", Due: nil, SnoozedUntil: nil},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := e.Extract(tc.item)
			if got.Completed != tc.want.Completed {
				t.Errorf("Completed = %v, want %v", got.Completed, tc.want.Completed)
			}
			if got.Project != tc.want.Project {
				t.Errorf("Project = %q, want %q", got.Project, tc.want.Project)
			}
			if !timePtrEqual(got.Due, tc.want.Due) {
				t.Errorf("Due = %v, want %v", got.Due, tc.want.Due)
			}
			if !timePtrEqual(got.SnoozedUntil, tc.want.SnoozedUntil) {
				t.Errorf("SnoozedUntil = %v, want %v", got.SnoozedUntil, tc.want.SnoozedUntil)
			}
		})
	}

	t.Run("extract uses the kind due binding", func(t *testing.T) {
		if err := e.RegisterKind("fake.kind", map[string]string{"due": "item.mirror.missing_since"}); err != nil {
			t.Fatalf("RegisterKind: %v", err)
		}
		bound := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
		item := &taskcorev1.Item{Kind: "fake.kind", Mirror: &taskcorev1.Mirror{MissingSince: ts(bound)}}
		got := e.Extract(item)
		if got.Due == nil || !got.Due.Equal(bound) {
			t.Errorf("Due = %v, want %v", got.Due, bound)
		}
	})
}

func timePtrEqual(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}
