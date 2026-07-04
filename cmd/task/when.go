package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var relDays = regexp.MustCompile(`^(\d+)d$`)

// parseWhen turns the CLI's WHEN grammar into a concrete time in now's
// location:
//
//	today             end of today (23:59:59)
//	tomorrow          end of tomorrow
//	Nd                N days from now, keeping the current time of day
//	YYYY-MM-DD        end of that day
//	YYYY-MM-DD HH:MM  that local time
//
// Date-only forms mean "due by end of day", hence 23:59:59.
func parseWhen(s string, now time.Time) (time.Time, error) {
	loc := now.Location()
	endOfDay := func(t time.Time) time.Time {
		y, m, d := t.Date()
		return time.Date(y, m, d, 23, 59, 59, 0, loc)
	}
	switch strings.ToLower(s) {
	case "today":
		return endOfDay(now), nil
	case "tomorrow":
		return endOfDay(now.AddDate(0, 0, 1)), nil
	}
	if m := relDays.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return time.Time{}, fmt.Errorf("cannot parse %q: %v", s, err)
		}
		return now.AddDate(0, 0, n), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, loc); err == nil {
		return endOfDay(t), nil
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04", s, loc); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("cannot parse %q: want today, tomorrow, Nd (e.g. 3d), YYYY-MM-DD, or \"YYYY-MM-DD HH:MM\"", s)
}
