package recur

import (
	"fmt"
	"strings"
	"time"
)

// FromNatural maps an everyday phrase to a canonical RRULE-subset string. It is
// case-insensitive and a leading "every" is optional throughout. The grammar
// (mirrored by the web frontend, so keep the two in lockstep):
//
//	"every day" / "daily"                  -> FREQ=DAILY
//	"every week" / "weekly"                -> FREQ=WEEKLY
//	"every month" / "monthly"              -> FREQ=MONTHLY
//	"every year" / "yearly"                -> FREQ=YEARLY
//	"every weekday"                        -> FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR
//	"every N days|weeks|months|years"      -> FREQ=...;INTERVAL=N
//	"every mon,wed" / "every monday, fri"  -> FREQ=WEEKLY;BYDAY=MO,WE
//
// Weekday names may be full or any prefix of at least three letters ("mon",
// "tues", "thursday"). It returns an error for anything it does not recognize,
// which lets callers fall back to Parse for an already-canonical rule.
func FromNatural(s string) (string, error) {
	orig := strings.TrimSpace(s)
	if orig == "" {
		return "", fmt.Errorf("recurrence is empty")
	}
	body := strings.ToLower(orig)
	body = strings.TrimSpace(strings.TrimPrefix(body, "every "))
	if body == "" {
		return "", fmt.Errorf("recurrence %q is incomplete", orig)
	}

	switch body {
	case "day", "daily":
		return Rule{Freq: Daily, Interval: 1}.String(), nil
	case "week", "weekly":
		return Rule{Freq: Weekly, Interval: 1}.String(), nil
	case "month", "monthly":
		return Rule{Freq: Monthly, Interval: 1}.String(), nil
	case "year", "yearly":
		return Rule{Freq: Yearly, Interval: 1}.String(), nil
	case "weekday", "weekdays":
		return Rule{
			Freq:     Weekly,
			Interval: 1,
			ByDay:    []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday},
		}.String(), nil
	}

	// "N days" / "N weeks" / ... (digits only: "+2" / "-2" are rejected).
	if fields := strings.Fields(body); len(fields) == 2 {
		if n, ok := atoiStrict(fields[0]); ok {
			if freq, ok := unitFreq(fields[1]); ok {
				if n < 1 {
					return "", fmt.Errorf("recurrence %q: interval must be >= 1", orig)
				}
				return Rule{Freq: freq, Interval: n}.String(), nil
			}
		}
	}

	// A weekday list: "mon,wed,fri" or "monday, friday".
	if days, ok := parseWeekdayList(body); ok {
		sortWeekdays(days)
		return Rule{Freq: Weekly, Interval: 1, ByDay: days}.String(), nil
	}

	return "", fmt.Errorf("recurrence %q not recognized", orig)
}

// unitFreq maps a (possibly plural) unit word to its frequency.
func unitFreq(word string) (Freq, bool) {
	switch strings.TrimSuffix(word, "s") {
	case "day":
		return Daily, true
	case "week":
		return Weekly, true
	case "month":
		return Monthly, true
	case "year":
		return Yearly, true
	}
	return 0, false
}

// parseWeekdayList parses a comma-separated list of weekday names; it fails
// (returns ok=false) unless every token resolves to exactly one weekday.
func parseWeekdayList(s string) ([]time.Weekday, bool) {
	tokens := strings.Split(s, ",")
	seen := map[time.Weekday]bool{}
	var days []time.Weekday
	for _, tok := range tokens {
		wd, ok := parseWeekdayName(strings.TrimSpace(tok))
		if !ok {
			return nil, false
		}
		if !seen[wd] {
			seen[wd] = true
			days = append(days, wd)
		}
	}
	if len(days) == 0 {
		return nil, false
	}
	return days, true
}

var weekdayNames = map[time.Weekday]string{
	time.Monday:    "monday",
	time.Tuesday:   "tuesday",
	time.Wednesday: "wednesday",
	time.Thursday:  "thursday",
	time.Friday:    "friday",
	time.Saturday:  "saturday",
	time.Sunday:    "sunday",
}

// parseWeekdayName resolves a full weekday name or a prefix of at least three
// letters. Three letters is enough to disambiguate all seven days.
func parseWeekdayName(tok string) (time.Weekday, bool) {
	if len(tok) < 3 {
		return 0, false
	}
	var match time.Weekday
	found := 0
	for wd, name := range weekdayNames {
		if strings.HasPrefix(name, tok) {
			match = wd
			found++
		}
	}
	if found == 1 {
		return match, true
	}
	return 0, false
}
