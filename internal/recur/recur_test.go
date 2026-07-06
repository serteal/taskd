package recur

import (
	"testing"
	"time"
)

func TestParseValid(t *testing.T) {
	tests := []struct {
		rule      string
		freq      Freq
		interval  int
		byDay     []time.Weekday
		canonical string
	}{
		{"FREQ=DAILY", Daily, 1, nil, "FREQ=DAILY"},
		{"FREQ=WEEKLY", Weekly, 1, nil, "FREQ=WEEKLY"},
		{"FREQ=MONTHLY", Monthly, 1, nil, "FREQ=MONTHLY"},
		{"FREQ=YEARLY", Yearly, 1, nil, "FREQ=YEARLY"},
		{"FREQ=DAILY;INTERVAL=3", Daily, 3, nil, "FREQ=DAILY;INTERVAL=3"},
		{"FREQ=WEEKLY;INTERVAL=2", Weekly, 2, nil, "FREQ=WEEKLY;INTERVAL=2"},
		// Case-insensitive keys/values; BYDAY sorted MO..SU regardless of input order.
		{"freq=weekly;byday=fr,mo,we", Weekly, 1, []time.Weekday{time.Monday, time.Wednesday, time.Friday}, "FREQ=WEEKLY;BYDAY=MO,WE,FR"},
		{"FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR", Weekly, 1, []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday}, "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"},
		{"FREQ=WEEKLY;INTERVAL=2;BYDAY=SA,SU", Weekly, 2, []time.Weekday{time.Saturday, time.Sunday}, "FREQ=WEEKLY;INTERVAL=2;BYDAY=SA,SU"},
		// Duplicate BYDAY codes dedupe.
		{"FREQ=WEEKLY;BYDAY=MO,MO", Weekly, 1, []time.Weekday{time.Monday}, "FREQ=WEEKLY;BYDAY=MO"},
	}
	for _, tt := range tests {
		r, err := Parse(tt.rule)
		if err != nil {
			t.Errorf("Parse(%q) err = %v, want ok", tt.rule, err)
			continue
		}
		if r.Freq != tt.freq || r.Interval != tt.interval {
			t.Errorf("Parse(%q) = freq %v interval %d, want %v/%d", tt.rule, r.Freq, r.Interval, tt.freq, tt.interval)
		}
		if !equalWeekdays(r.ByDay, tt.byDay) {
			t.Errorf("Parse(%q) byday = %v, want %v", tt.rule, r.ByDay, tt.byDay)
		}
		if got := r.String(); got != tt.canonical {
			t.Errorf("Parse(%q).String() = %q, want %q", tt.rule, got, tt.canonical)
		}
		// Canonical form round-trips.
		if _, err := Parse(r.String()); err != nil {
			t.Errorf("Parse(%q).String() = %q does not re-parse: %v", tt.rule, r.String(), err)
		}
	}
}

func TestParseInvalid(t *testing.T) {
	for _, rule := range []string{
		"",
		"   ",
		"INTERVAL=2",                // no FREQ
		"FREQ=HOURLY",               // unsupported freq
		"FREQ=DAILY;INTERVAL=0",     // interval < 1
		"FREQ=DAILY;INTERVAL=-1",    // negative
		"FREQ=DAILY;INTERVAL=+2",    // signed: digits only, no leading +
		"FREQ=DAILY;INTERVAL=two",   // not a number
		"FREQ=DAILY;BYDAY=MO",       // BYDAY on non-weekly
		"FREQ=MONTHLY;BYDAY=MO",     // BYDAY on non-weekly
		"FREQ=WEEKLY;BYDAY=XX",      // bad day code
		"FREQ=WEEKLY;BYDAY=",        // empty BYDAY
		"FREQ=DAILY;FREQ=WEEKLY",    // duplicate key
		"FREQ=DAILY;COUNT=5",        // unsupported key
		"FREQ=DAILY;UNTIL=20260101", // unsupported key
		"DAILY",                     // not KEY=VALUE
	} {
		if _, err := Parse(rule); err == nil {
			t.Errorf("Parse(%q) = ok, want error", rule)
		}
	}
}

func TestNext(t *testing.T) {
	// Anchor with a distinctive time of day so preservation is observable.
	at := func(y int, m time.Month, d, h, min int) time.Time {
		return time.Date(y, m, d, h, min, 0, 0, time.UTC)
	}
	tests := []struct {
		name string
		rule string
		from time.Time
		want time.Time
	}{
		{"daily", "FREQ=DAILY", at(2026, 1, 15, 9, 30), at(2026, 1, 16, 9, 30)},
		{"daily interval 3", "FREQ=DAILY;INTERVAL=3", at(2026, 1, 1, 8, 0), at(2026, 1, 4, 8, 0)},
		{"weekly", "FREQ=WEEKLY", at(2026, 7, 6, 9, 0), at(2026, 7, 13, 9, 0)},
		{"weekly interval 2", "FREQ=WEEKLY;INTERVAL=2", at(2026, 7, 6, 9, 0), at(2026, 7, 20, 9, 0)},
		// 2026-07-06 is a Monday: MO,WE,FR steps to WE then FR, then wraps to MO.
		{"byday mon->wed", "FREQ=WEEKLY;BYDAY=MO,WE,FR", at(2026, 7, 6, 9, 0), at(2026, 7, 8, 9, 0)},
		{"byday wed->fri", "FREQ=WEEKLY;BYDAY=MO,WE,FR", at(2026, 7, 8, 9, 0), at(2026, 7, 10, 9, 0)},
		{"byday fri wraps to mon", "FREQ=WEEKLY;BYDAY=MO,WE,FR", at(2026, 7, 10, 9, 0), at(2026, 7, 13, 9, 0)},
		// Interval 2 with BYDAY: exhausting the week jumps two weeks on.
		{"byday interval 2 wrap", "FREQ=WEEKLY;INTERVAL=2;BYDAY=MO", at(2026, 7, 6, 9, 0), at(2026, 7, 20, 9, 0)},
		{"monthly", "FREQ=MONTHLY", at(2026, 1, 15, 9, 0), at(2026, 2, 15, 9, 0)},
		// Jan 31 has no Feb 31: clamp to the last day of February (2026 is not a leap year).
		{"monthly clamps jan31->feb28", "FREQ=MONTHLY", at(2026, 1, 31, 9, 0), at(2026, 2, 28, 9, 0)},
		{"monthly interval crosses year", "FREQ=MONTHLY;INTERVAL=3", at(2026, 11, 30, 9, 0), at(2027, 2, 28, 9, 0)},
		{"yearly", "FREQ=YEARLY", at(2026, 3, 10, 9, 0), at(2027, 3, 10, 9, 0)},
		// Feb 29 2028 (leap) -> Feb 28 2029 (clamp).
		{"yearly clamps leap day", "FREQ=YEARLY", at(2028, 2, 29, 9, 0), at(2029, 2, 28, 9, 0)},
	}
	for _, tt := range tests {
		r, err := Parse(tt.rule)
		if err != nil {
			t.Fatalf("%s: Parse(%q): %v", tt.name, tt.rule, err)
		}
		got := r.Next(tt.from)
		if !got.Equal(tt.want) {
			t.Errorf("%s: Next(%v) = %v, want %v", tt.name, tt.from, got, tt.want)
		}
		if !got.After(tt.from) {
			t.Errorf("%s: Next(%v) = %v is not strictly after", tt.name, tt.from, got)
		}
	}
}

// TestNextGuardsRogueInterval: a hand-built Rule with a non-positive Interval
// (which Parse would never produce) is treated as Interval 1 and still
// advances, so a caller's fast-forward loop cannot spin forever.
func TestNextGuardsRogueInterval(t *testing.T) {
	from := time.Date(2026, 1, 15, 9, 0, 0, 0, time.UTC)
	for _, r := range []Rule{
		{Freq: Daily, Interval: 0},
		{Freq: Daily, Interval: -3},
		{Freq: Weekly, Interval: 0},
		{Freq: Monthly, Interval: -1},
		{Freq: Yearly, Interval: 0},
	} {
		got := r.Next(from)
		if !got.After(from) {
			t.Errorf("Next(%v) with rogue interval %+v = %v, did not advance", from, r, got)
		}
	}
	// Interval 0 daily behaves exactly like interval 1 (one day forward).
	if got := (Rule{Freq: Daily, Interval: 0}).Next(from); !got.Equal(from.AddDate(0, 0, 1)) {
		t.Errorf("Next daily interval 0 = %v, want one day forward %v", got, from.AddDate(0, 0, 1))
	}
}

// TestNextPreservesWallClockAcrossDST: recurrence follows local wall-clock
// time. A daily 09:00 task keeps 09:00 across both DST transitions even though
// its absolute UTC instant shifts by an hour, and monthly clamping runs in the
// local zone.
func TestNextPreservesWallClockAcrossDST(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("no America/New_York tzdata: %v", err)
	}
	daily, err := Parse("FREQ=DAILY")
	if err != nil {
		t.Fatal(err)
	}
	// Spring forward 2026: Mar 8, 02:00 EST -> 03:00 EDT. 09:00 EST is UTC 14:00;
	// the next day's 09:00 EDT is UTC 13:00 — one hour less than a naive +24h.
	spring := daily.Next(time.Date(2026, 3, 7, 9, 0, 0, 0, ny))
	if h, m := spring.Hour(), spring.Minute(); h != 9 || m != 0 {
		t.Errorf("spring-forward Next local time = %02d:%02d, want 09:00", h, m)
	}
	if want := time.Date(2026, 3, 8, 13, 0, 0, 0, time.UTC); !spring.UTC().Equal(want) {
		t.Errorf("spring-forward Next instant = %v, want %v (UTC shifted by DST)", spring.UTC(), want)
	}
	// Fall back 2026: Nov 1, 02:00 EDT -> 01:00 EST. 09:00 EDT is UTC 13:00; the
	// next day's 09:00 EST is UTC 14:00 — one hour more than a naive +24h.
	fall := daily.Next(time.Date(2026, 10, 31, 9, 0, 0, 0, ny))
	if h, m := fall.Hour(), fall.Minute(); h != 9 || m != 0 {
		t.Errorf("fall-back Next local time = %02d:%02d, want 09:00", h, m)
	}
	if want := time.Date(2026, 11, 1, 14, 0, 0, 0, time.UTC); !fall.UTC().Equal(want) {
		t.Errorf("fall-back Next instant = %v, want %v (UTC shifted by DST)", fall.UTC(), want)
	}
	// Monthly clamp near midnight local: Jan 31 23:30 local -> Feb 28 23:30 local.
	monthly, err := Parse("FREQ=MONTHLY")
	if err != nil {
		t.Fatal(err)
	}
	clamp := monthly.Next(time.Date(2026, 1, 31, 23, 30, 0, 0, ny))
	if y, m, d := clamp.Date(); y != 2026 || m != time.February || d != 28 {
		t.Errorf("monthly clamp date = %04d-%02d-%02d, want 2026-02-28", y, m, d)
	}
	if h, min := clamp.Hour(), clamp.Minute(); h != 23 || min != 30 {
		t.Errorf("monthly clamp local time = %02d:%02d, want 23:30", h, min)
	}
	if clamp.Location().String() != ny.String() {
		t.Errorf("monthly clamp location = %v, want %v", clamp.Location(), ny)
	}
}

func TestNextPreservesTimeOfDay(t *testing.T) {
	from := time.Date(2026, 3, 10, 14, 37, 45, 123456789, time.UTC)
	for _, rule := range []string{"FREQ=DAILY", "FREQ=WEEKLY", "FREQ=WEEKLY;BYDAY=MO,WE,FR", "FREQ=MONTHLY", "FREQ=YEARLY"} {
		r, err := Parse(rule)
		if err != nil {
			t.Fatal(err)
		}
		got := r.Next(from)
		if got.Hour() != from.Hour() || got.Minute() != from.Minute() || got.Second() != from.Second() || got.Nanosecond() != from.Nanosecond() {
			t.Errorf("%s: Next lost time of day: %v (want %02d:%02d:%02d.%09d)", rule, got, from.Hour(), from.Minute(), from.Second(), from.Nanosecond())
		}
	}
}

func TestFromNatural(t *testing.T) {
	tests := []struct{ in, want string }{
		{"every day", "FREQ=DAILY"},
		{"daily", "FREQ=DAILY"},
		{"Daily", "FREQ=DAILY"},
		{"every week", "FREQ=WEEKLY"},
		{"weekly", "FREQ=WEEKLY"},
		{"every month", "FREQ=MONTHLY"},
		{"monthly", "FREQ=MONTHLY"},
		{"every year", "FREQ=YEARLY"},
		{"yearly", "FREQ=YEARLY"},
		{"every weekday", "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"},
		{"weekday", "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"},
		{"every 2 days", "FREQ=DAILY;INTERVAL=2"},
		{"2 weeks", "FREQ=WEEKLY;INTERVAL=2"},
		{"every 3 months", "FREQ=MONTHLY;INTERVAL=3"},
		{"every 1 year", "FREQ=YEARLY"},
		{"every 10 years", "FREQ=YEARLY;INTERVAL=10"},
		// Weekday lists: full names or >=3-letter prefixes, any order/casing.
		{"mon,wed,fri", "FREQ=WEEKLY;BYDAY=MO,WE,FR"},
		{"every monday, wednesday", "FREQ=WEEKLY;BYDAY=MO,WE"},
		{"every Tue,Thu", "FREQ=WEEKLY;BYDAY=TU,TH"},
		{"sat,sun", "FREQ=WEEKLY;BYDAY=SA,SU"},
		{"thursday", "FREQ=WEEKLY;BYDAY=TH"},
	}
	for _, tt := range tests {
		got, err := FromNatural(tt.in)
		if err != nil {
			t.Errorf("FromNatural(%q) err = %v, want %q", tt.in, err, tt.want)
			continue
		}
		if got != tt.want {
			t.Errorf("FromNatural(%q) = %q, want %q", tt.in, got, tt.want)
		}
		// Everything it emits must parse.
		if _, err := Parse(got); err != nil {
			t.Errorf("FromNatural(%q) = %q does not Parse: %v", tt.in, got, err)
		}
	}
}

func TestFromNaturalRejects(t *testing.T) {
	for _, in := range []string{
		"",
		"   ",
		"every",
		"sometimes",
		"every fortnight",
		"every 0 days",
		"tu",      // < 3 letters
		"mon,xyz", // one bad token
		"every -2 weeks",
		"every +2 days", // signed interval: digits only, no leading +
		"FREQ=DAILY",    // canonical, not natural — falls through to reject so callers try Parse
	} {
		if got, err := FromNatural(in); err == nil {
			t.Errorf("FromNatural(%q) = %q, want error", in, got)
		}
	}
}

func TestHumanize(t *testing.T) {
	tests := []struct{ rule, want string }{
		{"FREQ=DAILY", "every day"},
		{"FREQ=WEEKLY", "every week"},
		{"FREQ=MONTHLY", "every month"},
		{"FREQ=YEARLY", "every year"},
		{"FREQ=DAILY;INTERVAL=2", "every 2 days"},
		{"FREQ=WEEKLY;INTERVAL=3", "every 3 weeks"},
		{"FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR", "every weekday"},
		// Weekday lists render as three-letter abbreviations, matching the web UI.
		{"FREQ=WEEKLY;BYDAY=MO,WE,FR", "every Mon, Wed, Fri"},
		{"FREQ=WEEKLY;BYDAY=SA,SU", "every Sat, Sun"},
		{"FREQ=WEEKLY;BYDAY=TH", "every Thu"},
		{"not a rule", "not a rule"}, // unparseable rules pass through verbatim
	}
	for _, tt := range tests {
		if got := Humanize(tt.rule); got != tt.want {
			t.Errorf("Humanize(%q) = %q, want %q", tt.rule, got, tt.want)
		}
	}
}

func equalWeekdays(a, b []time.Weekday) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
