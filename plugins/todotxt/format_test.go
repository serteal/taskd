package todotxt

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"
)

// TestParseSerializeRoundTrip covers the grammar and the invariant that
// Parse -> String -> Parse is a fixed point for well-formed lines.
func TestParseSerializeRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want Line
	}{
		{
			name: "plain",
			in:   "buy milk",
			want: Line{Description: "buy milk"},
		},
		{
			name: "priority",
			in:   "(A) call the dentist",
			want: Line{Priority: "A", Description: "call the dentist"},
		},
		{
			name: "creation date",
			in:   "2026-06-01 water the plants",
			want: Line{CreationDate: "2026-06-01", Description: "water the plants"},
		},
		{
			name: "completed both dates",
			in:   "x 2026-06-15 2026-06-01 write the report",
			want: Line{
				Completed:      true,
				CompletionDate: "2026-06-15",
				CreationDate:   "2026-06-01",
				Description:    "write the report",
			},
		},
		{
			name: "completed one date",
			in:   "x 2026-06-15 file taxes",
			want: Line{Completed: true, CompletionDate: "2026-06-15", Description: "file taxes"},
		},
		{
			name: "pri convention on completion",
			in:   "x 2026-06-15 submit invoice pri:B",
			want: Line{
				Completed:      true,
				CompletionDate: "2026-06-15",
				Description:    "submit invoice pri:B",
				Tags:           []Tag{{Key: "pri", Value: "B"}},
			},
		},
		{
			name: "due tag",
			in:   "pay rent due:2026-07-01",
			want: Line{
				Description: "pay rent due:2026-07-01",
				Tags:        []Tag{{Key: "due", Value: "2026-07-01"}},
				Due:         "2026-07-01",
			},
		},
		{
			name: "id tag",
			in:   "review the pr id:pr-42",
			want: Line{
				Description: "review the pr id:pr-42",
				Tags:        []Tag{{Key: "id", Value: "pr-42"}},
				ID:          "pr-42",
			},
		},
		{
			name: "unknown kv preserved",
			in:   "deploy service env:prod ref:v1.2",
			want: Line{
				Description: "deploy service env:prod ref:v1.2",
				Tags:        []Tag{{Key: "env", Value: "prod"}, {Key: "ref", Value: "v1.2"}},
			},
		},
		{
			name: "projects and contexts",
			in:   "email the boss +work @office about +budget",
			want: Line{
				Description: "email the boss +work @office about +budget",
				Projects:    []string{"work", "budget"},
				Contexts:    []string{"office"},
			},
		},
		{
			name: "everything at once",
			in:   "(C) 2026-06-01 ship +proj @ctx due:2026-07-04 id:x9 note:hi",
			want: Line{
				Priority:     "C",
				CreationDate: "2026-06-01",
				Description:  "ship +proj @ctx due:2026-07-04 id:x9 note:hi",
				Projects:     []string{"proj"},
				Contexts:     []string{"ctx"},
				Tags:         []Tag{{Key: "due", Value: "2026-07-04"}, {Key: "id", Value: "x9"}, {Key: "note", Value: "hi"}},
				Due:          "2026-07-04",
				ID:           "x9",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ws := parseLine(tc.in)
			if len(ws) != 0 {
				t.Fatalf("unexpected warnings: %v", ws)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parse(%q):\n got  %+v\n want %+v", tc.in, got, tc.want)
			}
			// String must round-trip back to an identical Line.
			s := got.String()
			reparsed, ws2 := parseLine(s)
			if len(ws2) != 0 {
				t.Fatalf("reparse warnings for %q: %v", s, ws2)
			}
			if !reflect.DeepEqual(reparsed, got) {
				t.Fatalf("round-trip mismatch:\n in       %q\n String() %q\n got  %+v\n want %+v", tc.in, s, reparsed, got)
			}
		})
	}
}

func TestSerializeCanonicalForm(t *testing.T) {
	cases := []struct {
		line Line
		want string
	}{
		{Line{Description: "buy milk"}, "buy milk"},
		{Line{Priority: "A", Description: "call dentist"}, "(A) call dentist"},
		{Line{Priority: "A", CreationDate: "2026-06-01", Description: "x"}, "(A) 2026-06-01 x"},
		{Line{Completed: true, CompletionDate: "2026-06-15", Description: "done"}, "x 2026-06-15 done"},
		{Line{Completed: true, CompletionDate: "2026-06-15", CreationDate: "2026-06-01", Description: "done"}, "x 2026-06-15 2026-06-01 done"},
	}
	for _, tc := range cases {
		if got := tc.line.String(); got != tc.want {
			t.Errorf("String(%+v) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

func TestMalformedDateWarning(t *testing.T) {
	// "2026-13-40" looks like a date but is not valid: it stays as plain text
	// and the line round-trips.
	l, ws := parseLine("x 2026-13-40 broken date task")
	if len(ws) == 0 || !strings.Contains(ws[0], "malformed date") {
		t.Fatalf("want a malformed-date warning, got %v", ws)
	}
	if l.CompletionDate != "" || l.CreationDate != "" {
		t.Fatalf("malformed date must not fill a date slot: %+v", l)
	}
	if l.Description != "2026-13-40 broken date task" {
		t.Fatalf("description = %q, want the date kept as text", l.Description)
	}
	if got := l.String(); got != "x 2026-13-40 broken date task" {
		t.Fatalf("String() = %q", got)
	}
}

func TestParseSkipsBlankAndComments(t *testing.T) {
	content := "buy milk\n\n   \n# a comment\n#another\ndo laundry\n"
	lines, ws := Parse([]byte(content))
	if len(ws) != 0 {
		t.Fatalf("unexpected warnings: %v", ws)
	}
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %+v", len(lines), lines)
	}
	if lines[0].Description != "buy milk" || lines[1].Description != "do laundry" {
		t.Fatalf("wrong lines: %+v", lines)
	}
}

func TestExternalID(t *testing.T) {
	// Tagged line: external_id is the id: tag value.
	tagged, _ := parseLine("review pr id:pr-42")
	if got := tagged.ExternalID(); got != "pr-42" {
		t.Errorf("tagged external_id = %q, want pr-42", got)
	}

	// Untagged line: h: + first 12 hex of sha256(description).
	untagged, _ := parseLine("buy milk +groceries")
	want := hashID("buy milk +groceries")
	if got := untagged.ExternalID(); got != want {
		t.Errorf("untagged external_id = %q, want %q", got, want)
	}

	// The id: tag value may itself start with "h:" (stabilized untagged line).
	stab, _ := parseLine("buy milk +groceries id:" + want)
	if got := stab.ExternalID(); got != want {
		t.Errorf("stabilized external_id = %q, want %q", got, want)
	}
}

func TestStripTagsTitle(t *testing.T) {
	l, _ := parseLine("x 2026-06-15 pay the bill +home @desk due:2026-07-01 id:b1 pri:A")
	if got := stripTags(l.Description); got != "pay the bill" {
		t.Errorf("title = %q, want %q", got, "pay the bill")
	}
}

func TestEffectivePriority(t *testing.T) {
	inc, _ := parseLine("(A) task")
	if got := inc.EffectivePriority(); got != "A" {
		t.Errorf("incomplete priority = %q, want A", got)
	}
	done, _ := parseLine("x 2026-06-15 task pri:C")
	if got := done.EffectivePriority(); got != "C" {
		t.Errorf("completed priority = %q, want C", got)
	}
	none, _ := parseLine("x 2026-06-15 task")
	if got := none.EffectivePriority(); got != "" {
		t.Errorf("no priority = %q, want empty", got)
	}
}

// hashID mirrors the frozen identity formula, independently, so the tests fail
// if the connector's formula ever drifts.
func hashID(desc string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(desc)))
	return "h:" + hex.EncodeToString(sum[:])[:12]
}
