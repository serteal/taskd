package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/serteal/taskd/internal/recur"
)

var (
	relOffset = regexp.MustCompile(`^(\d+)([dw])$`)
	clock24   = regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)
	clockAMPM = regexp.MustCompile(`^(\d{1,2})(?::(\d{2}))?(am|pm)$`)
)

// weekdayNames is indexed by time.Weekday (Sunday=0 … Saturday=6).
var weekdayNames = [...]string{
	"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday",
}

// parseWhen turns the CLI's WHEN grammar into a concrete time in now's
// location. It parses the (possibly multi-word) string word by word into an
// optional date part and an optional time part, then combines them:
//
//	today             end of today (23:59:59)
//	tomorrow          end of tomorrow
//	<weekday>         next occurrence strictly after today (mon, tuesday, …)
//	next <weekday>    an alias for the bare weekday (same next occurrence)
//	Nd / Nw           N days / N weeks from now, keeping the current time of day
//	in N days|weeks|months|years
//	                  N units from now; months and years clamp the day (Jan 31 +
//	                  1 month = Feb 28, not Mar 3)
//	YYYY-MM-DD        that day
//	HH:MM             24-hour time of day
//	H(am|pm) / H:MM(am|pm)
//	                  12-hour time of day
//
// Date-only forms resolve to end of day. A time part instead pins that clock
// time onto the date part ("fri 3pm" = Friday 15:00, "tomorrow 9am" = tomorrow
// 09:00); a bare time is today at that time, even if already past. Date and
// time may appear in either order — the last date wins and the last time wins.
func parseWhen(s string, now time.Time) (time.Time, error) {
	loc := now.Location()
	words := strings.Fields(strings.ToLower(strings.TrimSpace(s)))
	if len(words) == 0 {
		return time.Time{}, errWhen(s)
	}

	var (
		haveDate  bool
		dyear     int
		dmonth    time.Month
		dday      int
		keepClock bool // date form keeps now's time of day rather than end of day

		haveTime   bool
		thour, tmn int
	)
	setDate := func(t time.Time, keep bool) {
		dyear, dmonth, dday = t.Date()
		keepClock = keep
		haveDate = true
	}

	for i := 0; i < len(words); i++ {
		w := words[i]

		// "next <weekday>": an alias for the bare weekday — the same next
		// occurrence strictly after today, not a week further out.
		if w == "next" && i+1 < len(words) {
			wd, ok := parseWeekday(words[i+1])
			if !ok {
				return time.Time{}, errWhen(s)
			}
			setDate(nextWeekday(now, wd), false)
			i++
			continue
		}
		// "in N days|weeks|months|years". Months and years clamp the day like
		// recurrence (Jan 31 + 1 month = Feb 28), never overflowing the month.
		if w == "in" && i+2 < len(words) {
			n, err := strconv.Atoi(words[i+1])
			if err != nil || n < 0 {
				return time.Time{}, errWhen(s)
			}
			switch strings.TrimSuffix(words[i+2], "s") {
			case "day":
				setDate(now.AddDate(0, 0, n), true)
			case "week":
				setDate(now.AddDate(0, 0, 7*n), true)
			case "month":
				setDate(recur.AddMonthsClamped(now, n), true)
			case "year":
				setDate(recur.AddMonthsClamped(now, 12*n), true)
			default:
				return time.Time{}, errWhen(s)
			}
			i += 2
			continue
		}

		switch w {
		case "today":
			setDate(now, false)
			continue
		case "tomorrow":
			setDate(now.AddDate(0, 0, 1), false)
			continue
		}
		if wd, ok := parseWeekday(w); ok {
			setDate(nextWeekday(now, wd), false)
			continue
		}
		if m := relOffset.FindStringSubmatch(w); m != nil {
			n, _ := strconv.Atoi(m[1])
			if m[2] == "w" {
				n *= 7
			}
			setDate(now.AddDate(0, 0, n), true)
			continue
		}
		if t, err := time.ParseInLocation("2006-01-02", w, loc); err == nil {
			setDate(t, false)
			continue
		}
		if hh, mm, ok := parseClock(w); ok {
			thour, tmn = hh, mm
			haveTime = true
			continue
		}
		return time.Time{}, errWhen(s)
	}

	if !haveDate && !haveTime {
		return time.Time{}, errWhen(s)
	}
	if !haveDate {
		dyear, dmonth, dday = now.Date()
	}
	switch {
	case haveTime:
		return time.Date(dyear, dmonth, dday, thour, tmn, 0, 0, loc), nil
	case keepClock:
		return time.Date(dyear, dmonth, dday, now.Hour(), now.Minute(), now.Second(), now.Nanosecond(), loc), nil
	default:
		return time.Date(dyear, dmonth, dday, 23, 59, 59, 0, loc), nil
	}
}

// parseWeekday matches a full weekday name or any prefix of at least three
// letters that names exactly one weekday ("mon", "tue", "thurs", …).
func parseWeekday(w string) (time.Weekday, bool) {
	if len(w) < 3 {
		return 0, false
	}
	match := -1
	for i, name := range weekdayNames {
		if strings.HasPrefix(name, w) {
			if match != -1 {
				return 0, false // ambiguous prefix
			}
			match = i
		}
	}
	if match == -1 {
		return 0, false
	}
	return time.Weekday(match), true
}

// nextWeekday returns the next date whose weekday is target, strictly after
// now's day (so naming today's weekday means a week out).
func nextWeekday(now time.Time, target time.Weekday) time.Time {
	delta := (int(target) - int(now.Weekday()) + 7) % 7
	if delta == 0 {
		delta = 7
	}
	return now.AddDate(0, 0, delta)
}

// parseClock parses a time of day: "HH:MM" (24-hour), "H(am|pm)", or
// "H:MM(am|pm)". Out-of-range values do not match.
func parseClock(w string) (hour, min int, ok bool) {
	if m := clockAMPM.FindStringSubmatch(w); m != nil {
		h, _ := strconv.Atoi(m[1])
		if h < 1 || h > 12 {
			return 0, 0, false
		}
		mm := 0
		if m[2] != "" {
			mm, _ = strconv.Atoi(m[2])
			if mm > 59 {
				return 0, 0, false
			}
		}
		switch {
		case m[3] == "pm" && h != 12:
			h += 12
		case m[3] == "am" && h == 12:
			h = 0
		}
		return h, mm, true
	}
	if m := clock24.FindStringSubmatch(w); m != nil {
		h, _ := strconv.Atoi(m[1])
		mm, _ := strconv.Atoi(m[2])
		if h > 23 || mm > 59 {
			return 0, 0, false
		}
		return h, mm, true
	}
	return 0, 0, false
}

func errWhen(s string) error {
	return fmt.Errorf(`cannot parse %q as a time: want e.g. today, tomorrow, friday, "next friday", 3d, 2w, "in 2 weeks", 2026-12-24, "fri 3pm", or "tomorrow 9am"`, s)
}
