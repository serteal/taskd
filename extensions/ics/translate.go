// translate.go converts an iCalendar feed into ExternalTasks and upserts
// them.
//
// Window: only VEVENTs whose start falls in [now-24h, now+30d] are sent, and
// the batch is a full snapshot — the window IS this calendar's complete
// state, so events that drift out of it are pruned by the server. Per the
// API's ownership rules the feed owns title, due_time, completed_time, and
// external_data; labels and notes stay with the user (apply_labels only
// adds). A past event (end < now) is reported as completed.
//
// Recurrence: RRULEs are expanded within the window (EXDATE honored; RDATE
// ignored — ad-hoc extra occurrence dates are rare and rrule-go cannot merge
// them into a rule). RECURRENCE-ID override events are not special-cased;
// they pass through as standalone events.

package main

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	ics "github.com/arran4/golang-ical"
	rrule "github.com/teambition/rrule-go"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskpb "github.com/serteal/taskd/gen/task"
	"github.com/serteal/taskd/gen/task/taskconnect"
)

const (
	icsFetchTimeout   = 30 * time.Second
	icsWindowPast     = 24 * time.Hour
	icsWindowFuture   = 30 * 24 * time.Hour
	icsMaxOccurrences = 100 // per series; runaway guard for pathological RRULEs
)

// syncCalendar fetches one calendar, converts its events, and upserts them
// as the calendar's full snapshot.
func syncCalendar(ctx context.Context, tc taskconnect.TaskServiceClient, cal calendar, now func() time.Time) error {
	source := "ics:" + cal.Name
	parsed, err := fetchCalendar(ctx, cal)
	if err != nil {
		return fmt.Errorf("%s: %w", source, err)
	}
	tasks, err := icsToTasks(parsed, now())
	if err != nil {
		return fmt.Errorf("%s: %w", source, err)
	}
	if _, err := tc.UpsertExternalTasks(ctx, connect.NewRequest(&taskpb.UpsertExternalTasksRequest{
		Source:       source,
		Tasks:        tasks,
		ApplyLabels:  cal.Labels,
		FullSnapshot: true,
	})); err != nil {
		return fmt.Errorf("%s: upsert: %w", source, err)
	}
	return nil
}

func fetchCalendar(ctx context.Context, cal calendar) (*ics.Calendar, error) {
	if cal.Path != "" {
		b, err := os.ReadFile(cal.Path)
		if err != nil {
			return nil, err
		}
		return ics.ParseCalendar(bytes.NewReader(b))
	}
	ctx, cancel := context.WithTimeout(ctx, icsFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cal.URL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", cal.URL, resp.Status)
	}
	return ics.ParseCalendar(resp.Body)
}

func icsToTasks(cal *ics.Calendar, now time.Time) ([]*taskpb.ExternalTask, error) {
	windowStart := now.Add(-icsWindowPast)
	windowEnd := now.Add(icsWindowFuture)
	var tasks []*taskpb.ExternalTask
	for _, ev := range cal.Events() {
		start, err := ev.GetStartAt()
		if err != nil {
			// No usable DTSTART (e.g. a cancellation stub): nothing to schedule.
			continue
		}
		end := start
		if e, err := ev.GetEndAt(); err == nil {
			end = e
		}
		if rr := ev.GetProperty(ics.ComponentPropertyRrule); rr != nil {
			occs, err := expandICSRule(ev, rr.Value, start, windowStart, windowEnd)
			if err != nil {
				return nil, fmt.Errorf("event %q: %w", ev.Id(), err)
			}
			dur := end.Sub(start)
			for _, o := range occs {
				t, err := icsTask(ev, o, o.Add(dur), true, now)
				if err != nil {
					return nil, err
				}
				tasks = append(tasks, t)
			}
			continue
		}
		if start.Before(windowStart) || start.After(windowEnd) {
			continue
		}
		t, err := icsTask(ev, start, end, false, now)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	slices.SortFunc(tasks, func(a, b *taskpb.ExternalTask) int {
		return cmp.Compare(a.GetExternalRef(), b.GetExternalRef())
	})
	// external_ref must be unique within the batch (task.proto); feeds with
	// duplicate UIDs (typically RECURRENCE-ID overrides) keep the first entry.
	tasks = slices.CompactFunc(tasks, func(a, b *taskpb.ExternalTask) bool {
		return a.GetExternalRef() == b.GetExternalRef()
	})
	return tasks, nil
}

// expandICSRule returns the series' occurrence starts within the window,
// skipping EXDATE-excluded ones.
func expandICSRule(ev *ics.VEvent, rule string, start, windowStart, windowEnd time.Time) ([]time.Time, error) {
	opt, err := rrule.StrToROption(rule)
	if err != nil {
		return nil, fmt.Errorf("parsing RRULE: %w", err)
	}
	opt.Dtstart = start
	r, err := rrule.NewRRule(*opt)
	if err != nil {
		return nil, fmt.Errorf("building RRULE: %w", err)
	}
	occs := r.Between(windowStart, windowEnd, true)
	if len(occs) > icsMaxOccurrences {
		occs = occs[:icsMaxOccurrences]
	}
	exdates, err := ev.GetExDates()
	if err != nil {
		return nil, fmt.Errorf("parsing EXDATE: %w", err)
	}
	if len(exdates) == 0 {
		return occs, nil
	}
	kept := occs[:0]
	for _, o := range occs {
		if !slices.ContainsFunc(exdates, o.Equal) {
			kept = append(kept, o)
		}
	}
	return kept, nil
}

func icsTask(ev *ics.VEvent, start, end time.Time, occurrence bool, now time.Time) (*taskpb.ExternalTask, error) {
	uid := strings.ToValidUTF8(ev.Id(), string(utf8.RuneError))
	ref := uid
	if occurrence {
		ref = uid + "/" + start.Format(time.RFC3339)
	}
	title := icsText(ev, ics.ComponentPropertySummary)
	if title == "" {
		title = "(untitled event)"
	}
	t := &taskpb.ExternalTask{
		ExternalRef: ref,
		Title:       title,
		DueTime:     timestamppb.New(start),
	}
	if end.Before(now) {
		t.CompletedTime = timestamppb.New(end)
	}
	data := map[string]any{
		"all_day": icsIsAllDay(ev),
		"start":   start.Format(time.RFC3339),
		"end":     end.Format(time.RFC3339),
	}
	for k, v := range map[string]string{
		"uid":         uid,
		"location":    icsText(ev, ics.ComponentPropertyLocation),
		"description": icsText(ev, ics.ComponentPropertyDescription),
	} {
		if v != "" {
			data[k] = v
		}
	}
	st, err := structpb.NewStruct(data)
	if err != nil {
		return nil, fmt.Errorf("event %q: external_data: %w", uid, err)
	}
	t.ExternalData = st
	return t, nil
}

// icsText reads a TEXT property unescaped (golang-ical keeps the raw escaped
// value in .Value) and coerced to valid UTF-8 so it can enter a protobuf.
func icsText(ev *ics.VEvent, prop ics.ComponentProperty) string {
	p := ev.GetProperty(prop)
	if p == nil {
		return ""
	}
	return strings.ToValidUTF8(ics.FromText(p.Value), string(utf8.RuneError))
}

func icsIsAllDay(ev *ics.VEvent) bool {
	p := ev.GetProperty(ics.ComponentPropertyDtStart)
	if p == nil {
		return false
	}
	for _, v := range p.ICalParameters[string(ics.ParameterValue)] {
		if v == string(ics.ValueDataTypeDate) {
			return true
		}
	}
	return false
}
