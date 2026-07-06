package main

import (
	"testing"
	"time"
)

func TestParseWhen(t *testing.T) {
	// A Monday, so weekday examples match the spec ("today is Mon").
	now := time.Date(2026, time.July, 6, 10, 30, 0, 0, time.Local)
	d := func(y int, m time.Month, day, h, min, s int) time.Time {
		return time.Date(y, m, day, h, min, s, 0, time.Local)
	}
	cases := []struct {
		in      string
		want    time.Time
		wantErr bool
	}{
		// Existing forms.
		{in: "today", want: d(2026, time.July, 6, 23, 59, 59)},
		{in: "Today", want: d(2026, time.July, 6, 23, 59, 59)},
		{in: "tomorrow", want: d(2026, time.July, 7, 23, 59, 59)},
		{in: "0d", want: now},
		{in: "3d", want: d(2026, time.July, 9, 10, 30, 0)},
		{in: "30d", want: d(2026, time.August, 5, 10, 30, 0)},
		{in: "2026-12-24", want: d(2026, time.December, 24, 23, 59, 59)},
		{in: "2026-12-24 08:15", want: d(2026, time.December, 24, 8, 15, 0)},

		// Weeks: Nw, like Nd, keeps the current time of day.
		{in: "1w", want: d(2026, time.July, 13, 10, 30, 0)},
		{in: "2w", want: d(2026, time.July, 20, 10, 30, 0)},

		// Weekdays: next occurrence strictly after today; prefixes ok.
		{in: "friday", want: d(2026, time.July, 10, 23, 59, 59)},
		{in: "fri", want: d(2026, time.July, 10, 23, 59, 59)},
		{in: "monday", want: d(2026, time.July, 13, 23, 59, 59)}, // today is Monday → a week out
		{in: "mon", want: d(2026, time.July, 13, 23, 59, 59)},
		{in: "thu", want: d(2026, time.July, 9, 23, 59, 59)},
		{in: "sunday", want: d(2026, time.July, 12, 23, 59, 59)},

		// next <weekday>: an alias for the bare weekday (same next occurrence).
		{in: "next friday", want: d(2026, time.July, 10, 23, 59, 59)},
		{in: "next monday", want: d(2026, time.July, 13, 23, 59, 59)}, // today is Monday → a week out
		{in: "next thu", want: d(2026, time.July, 9, 23, 59, 59)},

		// in N units.
		{in: "in 3 days", want: d(2026, time.July, 9, 10, 30, 0)},
		{in: "in 1 day", want: d(2026, time.July, 7, 10, 30, 0)},
		{in: "in 2 weeks", want: d(2026, time.July, 20, 10, 30, 0)},
		{in: "in 1 month", want: d(2026, time.August, 6, 10, 30, 0)},
		{in: "in 2 months", want: d(2026, time.September, 6, 10, 30, 0)},
		{in: "in 1 year", want: d(2027, time.July, 6, 10, 30, 0)},
		{in: "in 2 years", want: d(2028, time.July, 6, 10, 30, 0)},

		// Times of day, 24-hour and 12-hour.
		{in: "3pm", want: d(2026, time.July, 6, 15, 0, 0)},
		{in: "9am", want: d(2026, time.July, 6, 9, 0, 0)},
		{in: "12am", want: d(2026, time.July, 6, 0, 0, 0)},
		{in: "12pm", want: d(2026, time.July, 6, 12, 0, 0)},
		{in: "3:30pm", want: d(2026, time.July, 6, 15, 30, 0)},
		{in: "08:15", want: d(2026, time.July, 6, 8, 15, 0)},
		{in: "23:59", want: d(2026, time.July, 6, 23, 59, 0)},
		// A bare time is today at that time, even when already past (now 10:30).
		{in: "9:00", want: d(2026, time.July, 6, 9, 0, 0)},

		// Date + time combine; the time pins the clock, not end of day.
		{in: "fri 3pm", want: d(2026, time.July, 10, 15, 0, 0)},
		{in: "tomorrow 9am", want: d(2026, time.July, 7, 9, 0, 0)},
		{in: "2026-12-24 3pm", want: d(2026, time.December, 24, 15, 0, 0)},
		{in: "next friday 8:30am", want: d(2026, time.July, 10, 8, 30, 0)},
		{in: "in 2 weeks 5pm", want: d(2026, time.July, 20, 17, 0, 0)},

		// Order independence: date and time may appear either way round.
		{in: "3pm fri", want: d(2026, time.July, 10, 15, 0, 0)},
		{in: "9am tomorrow", want: d(2026, time.July, 7, 9, 0, 0)},

		// Last date wins, last time wins.
		{in: "friday monday", want: d(2026, time.July, 13, 23, 59, 59)},
		{in: "fri 3pm 5pm", want: d(2026, time.July, 10, 17, 0, 0)},
		{in: "3pm friday tomorrow 9am", want: d(2026, time.July, 7, 9, 0, 0)},

		// Errors.
		{in: "", wantErr: true},
		{in: "next week", wantErr: true},
		{in: "-3d", wantErr: true},
		{in: "3h", wantErr: true},
		{in: "2026-13-01", wantErr: true},
		{in: "2026-12-24 25:00", wantErr: true},
		{in: "13pm", wantErr: true},  // 12-hour hour out of range
		{in: "24:00", wantErr: true}, // 24-hour hour out of range
		{in: "in days", wantErr: true},
		{in: "in 2 fortnights", wantErr: true},
		{in: "next bogusday", wantErr: true},
	}
	for _, c := range cases {
		got, err := parseWhen(c.in, now)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseWhen(%q) = %v, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseWhen(%q): %v", c.in, err)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("parseWhen(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestParseWhenMonthYearClamp: "in N months"/"in N years" clamp the day to the
// target month's length instead of overflowing (Go's AddDate would normalize
// Jan 31 + 1 month to Mar 3).
func TestParseWhenMonthYearClamp(t *testing.T) {
	d := func(y int, m time.Month, day, h, min, s int) time.Time {
		return time.Date(y, m, day, h, min, s, 0, time.Local)
	}
	cases := []struct {
		in   string
		now  time.Time
		want time.Time
	}{
		// Jan 31 + 1 month clamps to Feb 28 (2026 is not a leap year), keeping
		// the current time of day.
		{in: "in 1 month", now: d(2026, time.January, 31, 9, 15, 0), want: d(2026, time.February, 28, 9, 15, 0)},
		// Jan 31 + 1 month in a leap year clamps to Feb 29.
		{in: "in 1 month", now: d(2028, time.January, 31, 9, 15, 0), want: d(2028, time.February, 29, 9, 15, 0)},
		// Feb 29 (leap) + 1 year clamps to Feb 28 via 12 months.
		{in: "in 1 year", now: d(2028, time.February, 29, 8, 0, 0), want: d(2029, time.February, 28, 8, 0, 0)},
	}
	for _, c := range cases {
		got, err := parseWhen(c.in, c.now)
		if err != nil {
			t.Errorf("parseWhen(%q, %v): %v", c.in, c.now, err)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("parseWhen(%q, %v) = %v, want clamped %v", c.in, c.now, got, c.want)
		}
	}
}
