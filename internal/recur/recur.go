// Package recur handles task recurrence: a canonical RRULE subset with just
// enough grammar for a personal tracker. Parse validates a rule and Next
// computes the following occurrence, so the server can roll a completed
// recurring task forward. FromNatural maps everyday phrases ("every weekday",
// "2 weeks") onto the same canonical form the web frontend mirrors.
//
// The subset is deliberately small — anything outside it is rejected:
//
//	FREQ=DAILY|WEEKLY|MONTHLY|YEARLY   (required)
//	INTERVAL=n                          (optional, n>=1)
//	BYDAY=MO,TU,WE,TH,FR,SA,SU          (optional, WEEKLY only)
//
// Expand is the exception: it delegates to a full iCalendar RRULE engine for
// the ics syncer's windowed feed expansion, which needs COUNT/UNTIL/etc. that
// task recurrence never uses.
package recur

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Freq is a recurrence frequency.
type Freq int

const (
	Daily Freq = iota
	Weekly
	Monthly
	Yearly
)

// Rule is a parsed, validated recurrence.
type Rule struct {
	Freq     Freq
	Interval int            // always >= 1
	ByDay    []time.Weekday // WEEKLY only; sorted MO..SU; empty means every week
}

// weekdayCodes maps the canonical two-letter BYDAY codes to weekdays.
var weekdayCodes = map[string]time.Weekday{
	"MO": time.Monday,
	"TU": time.Tuesday,
	"WE": time.Wednesday,
	"TH": time.Thursday,
	"FR": time.Friday,
	"SA": time.Saturday,
	"SU": time.Sunday,
}

// codeOf is the inverse of weekdayCodes.
var codeOf = map[time.Weekday]string{
	time.Monday:    "MO",
	time.Tuesday:   "TU",
	time.Wednesday: "WE",
	time.Thursday:  "TH",
	time.Friday:    "FR",
	time.Saturday:  "SA",
	time.Sunday:    "SU",
}

var freqNames = map[Freq]string{
	Daily:   "DAILY",
	Weekly:  "WEEKLY",
	Monthly: "MONTHLY",
	Yearly:  "YEARLY",
}

// isoIndex orders weekdays Monday=0 .. Sunday=6 for week arithmetic and BYDAY
// sorting (Go's time.Weekday puts Sunday first).
func isoIndex(d time.Weekday) int { return (int(d) + 6) % 7 }

// Parse validates a canonical RRULE-subset string. Keys are case-insensitive;
// unknown keys, duplicate keys, unsupported values, and BYDAY on a non-weekly
// rule are all errors.
func Parse(rule string) (Rule, error) {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return Rule{}, fmt.Errorf("recurrence is empty")
	}
	var (
		r        Rule
		haveFreq bool
		seen     = map[string]bool{}
	)
	r.Interval = 1
	for _, part := range strings.Split(rule, ";") {
		key, val, ok := strings.Cut(part, "=")
		if !ok {
			return Rule{}, fmt.Errorf("recurrence %q: %q is not KEY=VALUE", rule, part)
		}
		key = strings.ToUpper(strings.TrimSpace(key))
		val = strings.TrimSpace(val)
		if seen[key] {
			return Rule{}, fmt.Errorf("recurrence %q: duplicate %s", rule, key)
		}
		seen[key] = true
		switch key {
		case "FREQ":
			f, ok := parseFreq(val)
			if !ok {
				return Rule{}, fmt.Errorf("recurrence %q: FREQ=%s is not one of DAILY, WEEKLY, MONTHLY, YEARLY", rule, val)
			}
			r.Freq, haveFreq = f, true
		case "INTERVAL":
			n, ok := atoiStrict(val)
			if !ok || n < 1 {
				return Rule{}, fmt.Errorf("recurrence %q: INTERVAL must be an integer >= 1, got %q", rule, val)
			}
			r.Interval = n
		case "BYDAY":
			days, err := parseByDay(val)
			if err != nil {
				return Rule{}, fmt.Errorf("recurrence %q: %w", rule, err)
			}
			r.ByDay = days
		default:
			return Rule{}, fmt.Errorf("recurrence %q: unsupported key %s", rule, key)
		}
	}
	if !haveFreq {
		return Rule{}, fmt.Errorf("recurrence %q: FREQ is required", rule)
	}
	if len(r.ByDay) > 0 && r.Freq != Weekly {
		return Rule{}, fmt.Errorf("recurrence %q: BYDAY is only allowed with FREQ=WEEKLY", rule)
	}
	return r, nil
}

// atoiStrict parses s as a base-10 non-negative integer, accepting ASCII digits
// only. Unlike strconv.Atoi it rejects a leading sign, so "+2" and "-2" are not
// valid intervals (parity with the web parser's digits-only rule).
func atoiStrict(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

func parseFreq(s string) (Freq, bool) {
	switch strings.ToUpper(s) {
	case "DAILY":
		return Daily, true
	case "WEEKLY":
		return Weekly, true
	case "MONTHLY":
		return Monthly, true
	case "YEARLY":
		return Yearly, true
	}
	return 0, false
}

// parseByDay parses a comma list of BYDAY codes into sorted, deduplicated
// weekdays.
func parseByDay(s string) ([]time.Weekday, error) {
	seen := map[time.Weekday]bool{}
	var days []time.Weekday
	for _, code := range strings.Split(s, ",") {
		code = strings.ToUpper(strings.TrimSpace(code))
		wd, ok := weekdayCodes[code]
		if !ok {
			return nil, fmt.Errorf("BYDAY %q is not one of MO,TU,WE,TH,FR,SA,SU", code)
		}
		if !seen[wd] {
			seen[wd] = true
			days = append(days, wd)
		}
	}
	if len(days) == 0 {
		return nil, fmt.Errorf("BYDAY must list at least one day")
	}
	sortWeekdays(days)
	return days, nil
}

func sortWeekdays(days []time.Weekday) {
	// Small fixed set: insertion sort by ISO index keeps MO..SU order.
	for i := 1; i < len(days); i++ {
		for j := i; j > 0 && isoIndex(days[j]) < isoIndex(days[j-1]); j-- {
			days[j], days[j-1] = days[j-1], days[j]
		}
	}
}

// String renders the rule back to its canonical form (INTERVAL omitted when 1,
// BYDAY listed in MO..SU order). Parse(r.String()) round-trips.
func (r Rule) String() string {
	var b strings.Builder
	b.WriteString("FREQ=")
	b.WriteString(freqNames[r.Freq])
	if r.Interval > 1 {
		b.WriteString(";INTERVAL=")
		b.WriteString(strconv.Itoa(r.Interval))
	}
	if len(r.ByDay) > 0 {
		codes := make([]string, len(r.ByDay))
		for i, d := range r.ByDay {
			codes[i] = codeOf[d]
		}
		b.WriteString(";BYDAY=")
		b.WriteString(strings.Join(codes, ","))
	}
	return b.String()
}

// Next returns the first occurrence strictly after t, keeping t's local
// wall-clock time of day (and thus its location — recurrence preserves local
// wall-clock time, so the caller's timezone is authoritative and the absolute
// instant shifts across DST). Monthly and yearly advance by whole months and
// clamp the day to the target month's length (Jan 31 -> Feb 28), matching how a
// calendar rolls a month-day that does not exist forward.
func (r Rule) Next(t time.Time) time.Time {
	// A validated Rule always has Interval >= 1, but guard a hand-built one so a
	// zero/negative interval can never produce a non-advancing loop in callers.
	if r.Interval < 1 {
		r.Interval = 1
	}
	switch r.Freq {
	case Weekly:
		if len(r.ByDay) > 0 {
			return r.nextWeeklyByDay(t)
		}
		return t.AddDate(0, 0, 7*r.Interval)
	case Monthly:
		return AddMonthsClamped(t, r.Interval)
	case Yearly:
		return AddMonthsClamped(t, 12*r.Interval)
	default: // Daily
		return t.AddDate(0, 0, r.Interval)
	}
}

// nextWeeklyByDay finds the next BYDAY weekday after t. Within t's own week
// (Monday-anchored) it stays in-week; once the week's listed days are
// exhausted it jumps Interval weeks forward to the first listed day.
func (r Rule) nextWeeklyByDay(t time.Time) time.Time {
	cur := isoIndex(t.Weekday())
	for _, d := range r.ByDay {
		if bi := isoIndex(d); bi > cur {
			return t.AddDate(0, 0, bi-cur)
		}
	}
	// No later day this week: advance to the next active week's first day.
	daysToNextMonday := 7 - cur
	weekJump := daysToNextMonday + (r.Interval-1)*7
	return t.AddDate(0, 0, weekJump+isoIndex(r.ByDay[0]))
}

// AddMonthsClamped adds months to t, clamping the day to the last day of the
// target month so no overflow into the following month occurs (Jan 31 + 1 month
// = Feb 28, not Mar 3) and preserving t's clock and location. Exported so CLI
// month/year offsets clamp identically to recurrence.
func AddMonthsClamped(t time.Time, months int) time.Time {
	y, m, d := t.Date()
	total := int(m) - 1 + months
	ty := y + total/12
	tm := time.Month(total%12) + 1
	if dim := daysInMonth(ty, tm); d > dim {
		d = dim
	}
	return time.Date(ty, tm, d, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

// daysInMonth returns the number of days in the given month (day 0 of the next
// month is the last day of this one).
func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// Humanize renders a canonical rule as a short English phrase, or returns the
// rule verbatim if it does not parse.
func Humanize(rule string) string {
	r, err := Parse(rule)
	if err != nil {
		return rule
	}
	unit := map[Freq]string{Daily: "day", Weekly: "week", Monthly: "month", Yearly: "year"}[r.Freq]
	if r.Freq == Weekly && len(r.ByDay) > 0 {
		if isWeekdaySet(r.ByDay) {
			return "every weekday"
		}
		// Three-letter abbreviations ("Mon, Wed, Fri"), matching the web UI.
		names := make([]string, len(r.ByDay))
		for i, d := range r.ByDay {
			names[i] = d.String()[:3]
		}
		return "every " + strings.Join(names, ", ")
	}
	if r.Interval == 1 {
		return "every " + unit
	}
	return fmt.Sprintf("every %d %ss", r.Interval, unit)
}

// isWeekdaySet reports whether days is exactly Monday through Friday.
func isWeekdaySet(days []time.Weekday) bool {
	if len(days) != 5 {
		return false
	}
	want := []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday}
	for i, d := range days {
		if d != want[i] {
			return false
		}
	}
	return true
}
