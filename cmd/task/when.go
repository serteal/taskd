package main

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

var relWhen = regexp.MustCompile(`^(\d+)([dh])$`)

// parseWhen turns the CLI's WHEN grammar into a concrete time:
//
//	RFC3339                    exact instant
//	2006-01-02                 local midnight of that date
//	today                      end of today, local
//	tomorrow                   end of tomorrow, local
//	Nd / Nh                    now + N days / hours ("3d", "12h")
func parseWhen(s string, now time.Time) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	loc := now.Location()
	if t, err := time.ParseInLocation("2006-01-02", s, loc); err == nil {
		return t, nil
	}
	endOfDay := func(t time.Time) time.Time {
		y, m, d := t.Date()
		return time.Date(y, m, d, 23, 59, 59, 0, loc)
	}
	switch s {
	case "today":
		return endOfDay(now), nil
	case "tomorrow":
		return endOfDay(now.AddDate(0, 0, 1)), nil
	}
	if m := relWhen.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return time.Time{}, fmt.Errorf("cannot parse %q: %v", s, err)
		}
		switch m[2] {
		case "d":
			return now.AddDate(0, 0, n), nil
		case "h":
			return now.Add(time.Duration(n) * time.Hour), nil
		}
	}
	return time.Time{}, fmt.Errorf(
		"cannot parse %q: want RFC3339 (2026-07-03T17:00:00Z), a date (2026-07-03), \"today\", \"tomorrow\", or a relative offset like \"3d\" or \"12h\"", s)
}
