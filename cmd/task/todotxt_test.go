package main

import (
	"io"
	"slices"
	"strings"
	"testing"
	"time"
)

// Canonical lines must survive line → fields → line unchanged.
func TestTodoLineRoundTrip(t *testing.T) {
	lines := []string{
		"Call mom",
		"(A) Ship report +work due:2026-07-10",
		"(B) Water plants @home label:garden",
		"x 2026-07-01 Pay rent +home @phone",
		"x Take out trash",
		"Read paper label:reading label:someday",
		"x 2026-07-01 (C) Renew passport +admin @errands due:2026-06-30",
	}
	for _, line := range lines {
		tl, ok := parseTodoLine(line, io.Discard)
		if !ok {
			t.Errorf("parseTodoLine(%q) skipped a non-blank line", line)
			continue
		}
		if got := formatTodoLine(tl); got != line {
			t.Errorf("round trip of %q = %q", line, got)
		}
	}
}

func TestParseTodoLineFields(t *testing.T) {
	var warn strings.Builder
	tl, ok := parseTodoLine("x 2026-07-01 (A) Fix the roof +house @weekend due:2026-07-15 label:urgentish foo:bar", &warn)
	if !ok {
		t.Fatal("line skipped")
	}
	if tl.title != "Fix the roof" {
		t.Errorf("title = %q", tl.title)
	}
	if !tl.completed {
		t.Error("completed = false")
	}
	if want := time.Date(2026, time.July, 1, 12, 0, 0, 0, time.Local); !tl.completedAt.Equal(want) {
		t.Errorf("completedAt = %v, want %v", tl.completedAt, want)
	}
	if want := time.Date(2026, time.July, 15, 23, 59, 59, 0, time.Local); !tl.due.Equal(want) {
		t.Errorf("due = %v, want %v", tl.due, want)
	}
	wantLabels := []string{"p1", "project:house", "context:weekend", "urgentish"}
	if !slices.Equal(tl.labels, wantLabels) {
		t.Errorf("labels = %v, want %v", tl.labels, wantLabels)
	}
	if !strings.Contains(warn.String(), `"foo:bar"`) {
		t.Errorf("expected a warning about foo:bar, got %q", warn.String())
	}
}

func TestParseTodoLineDropsHighPriorities(t *testing.T) {
	var warn strings.Builder
	tl, ok := parseTodoLine("(D) sharpen pencils", &warn)
	if !ok {
		t.Fatal("line skipped")
	}
	if len(tl.labels) != 0 || tl.title != "sharpen pencils" {
		t.Errorf("got labels=%v title=%q", tl.labels, tl.title)
	}
	if !strings.Contains(warn.String(), "(D)") {
		t.Errorf("expected a warning about (D), got %q", warn.String())
	}
}

func TestParseTodoLineBlankAndBadDue(t *testing.T) {
	if _, ok := parseTodoLine("   ", io.Discard); ok {
		t.Error("blank line not skipped")
	}
	var warn strings.Builder
	tl, _ := parseTodoLine("thing due:soon", &warn)
	if !tl.due.IsZero() || tl.title != "thing" {
		t.Errorf("bad due survived: due=%v title=%q", tl.due, tl.title)
	}
	if !strings.Contains(warn.String(), "due:soon") {
		t.Errorf("expected a warning about due:soon, got %q", warn.String())
	}
}

// A completed line with no completion date keeps completed but no date; an
// incomplete parse ("x" glued to a word) stays a title.
func TestParseTodoLineCompletionEdges(t *testing.T) {
	tl, _ := parseTodoLine("x Take out trash", io.Discard)
	if !tl.completed || !tl.completedAt.IsZero() || tl.title != "Take out trash" {
		t.Errorf("got %+v", tl)
	}
	tl, _ = parseTodoLine("xylophone practice", io.Discard)
	if tl.completed || tl.title != "xylophone practice" {
		t.Errorf("got %+v", tl)
	}
}
