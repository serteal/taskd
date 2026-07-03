package main

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestWhen(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 3, 10, 30, 0, 0, loc)

	cases := []struct {
		in   string
		want time.Time
	}{
		{"2026-08-01T12:00:00Z", time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)},
		{"2026-08-01T12:00:00+02:00", time.Date(2026, 8, 1, 12, 0, 0, 0, time.FixedZone("", 2*3600))},
		{"2026-08-01", time.Date(2026, 8, 1, 0, 0, 0, 0, loc)}, // local midnight
		{"today", time.Date(2026, 7, 3, 23, 59, 59, 0, loc)},   // end of today
		{"tomorrow", time.Date(2026, 7, 4, 23, 59, 59, 0, loc)},
		{"3d", now.AddDate(0, 0, 3)},
		{"12h", now.Add(12 * time.Hour)},
	}
	for _, c := range cases {
		got, err := parseWhen(c.in, now)
		if err != nil {
			t.Errorf("parseWhen(%q) error: %v", c.in, err)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("parseWhen(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestWhenBad(t *testing.T) {
	now := time.Now()
	for _, in := range []string{"", "yesterday", "3x", "d", "-3d", "3.5d", "next week", "01/02/2026"} {
		if _, err := parseWhen(in, now); err == nil {
			t.Errorf("parseWhen(%q) succeeded, want error", in)
		} else if !strings.Contains(err.Error(), "cannot parse") {
			t.Errorf("parseWhen(%q) error %q lacks a helpful message", in, err)
		}
	}
}

func TestHumanDue(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 7, 3, 10, 0, 0, 0, loc)
	cases := []struct {
		due  time.Time
		want string
	}{
		{time.Date(2026, 7, 3, 23, 59, 0, 0, loc), "today"},
		{time.Date(2026, 7, 5, 0, 1, 0, 0, loc), "2d"},
		{time.Date(2026, 6, 30, 12, 0, 0, 0, loc), "-3d"},
	}
	for _, c := range cases {
		if got := humanDue(timestamppb.New(c.due), now); got != c.want {
			t.Errorf("humanDue(%v) = %q, want %q", c.due, got, c.want)
		}
	}
	if got := humanDue(nil, now); got != "" {
		t.Errorf("humanDue(nil) = %q, want empty", got)
	}
}
