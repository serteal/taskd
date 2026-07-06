package recur

import (
	"fmt"
	"time"

	rrule "github.com/teambition/rrule-go"
)

// Expand returns a series' occurrence starts within [windowStart, windowEnd],
// anchored at dtstart. Unlike Parse it accepts the full iCalendar RRULE grammar
// (COUNT, UNTIL, BYMONTHDAY, ...) via the rrule engine: it exists for the ics
// syncer, which expands arbitrary calendar feeds into a bounded window and does
// not need — or want — the task subset's restrictions.
func Expand(rule string, dtstart, windowStart, windowEnd time.Time) ([]time.Time, error) {
	opt, err := rrule.StrToROption(rule)
	if err != nil {
		return nil, fmt.Errorf("parsing RRULE: %w", err)
	}
	opt.Dtstart = dtstart
	r, err := rrule.NewRRule(*opt)
	if err != nil {
		return nil, fmt.Errorf("building RRULE: %w", err)
	}
	return r.Between(windowStart, windowEnd, true), nil
}
