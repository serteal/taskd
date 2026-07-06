package recur

import (
	"testing"
	"time"
)

// TestExpandWeeklyWindow mirrors the ics syncer's windowed expansion: a weekly
// series clipped to a month-long window. Expand accepts COUNT — full iCalendar
// grammar that the task subset's Parse deliberately rejects.
func TestExpandWeeklyWindow(t *testing.T) {
	if _, err := Parse("FREQ=WEEKLY;COUNT=10"); err == nil {
		t.Fatal("Parse should reject COUNT; Expand and Parse must be distinct engines")
	}

	start := time.Date(2026, 7, 6, 9, 0, 0, 0, time.UTC)
	windowStart := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)

	occs, err := Expand("FREQ=WEEKLY;COUNT=10", start, windowStart, windowEnd)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	want := []time.Time{
		time.Date(2026, 7, 6, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC),
	}
	if len(occs) != len(want) {
		t.Fatalf("Expand returned %d occurrences, want %d: %v", len(occs), len(want), occs)
	}
	for i := range want {
		if !occs[i].Equal(want[i]) {
			t.Errorf("occurrence %d = %v, want %v", i, occs[i], want[i])
		}
	}
}

func TestExpandDailyWindow(t *testing.T) {
	start := time.Date(2026, 7, 10, 8, 0, 0, 0, time.UTC)
	occs, err := Expand("FREQ=DAILY;INTERVAL=2", start,
		time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	// 7/10, 7/12, 7/14 fall in [7/10, 7/16); 7/16 is out (half-open at end here is
	// inclusive per rrule Between(inc=true), but 7/16 08:00 > windowEnd 00:00).
	if len(occs) != 3 {
		t.Fatalf("got %d occurrences, want 3: %v", len(occs), occs)
	}
}

func TestExpandInvalidRule(t *testing.T) {
	start := time.Date(2026, 7, 10, 8, 0, 0, 0, time.UTC)
	if _, err := Expand("FREQ=NONSENSE", start, start, start.AddDate(0, 1, 0)); err == nil {
		t.Error("Expand(bad rule) = ok, want error")
	}
}
