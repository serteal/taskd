// mock.go fabricates a deterministic, realistic week of calendar events. This
// is the ONLY part that would change to make the extension talk to a real
// Google Calendar: replace mockEvents with an API (or per-calendar ICS URL)
// fetch that emits the same ExternalTask shape. Everything else — main.go's
// wiring and the entire web half — stays identical.
//
// The external_data schema is IDENTICAL to the ics syncer's, so the shared
// calendar presenter and week view read both without special-casing:
//
//	start   RFC3339 string (also the due_time)
//	end     RFC3339 string
//	all_day bool
//	location    string, present only when set
//	description string, present only when set
//
// A past event (end < now) is reported completed, exactly like ics.

package main

import (
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskpb "github.com/serteal/taskd/gen/task"
)

// eventSpec is one templated event, positioned relative to the week's Monday.
type eventSpec struct {
	day       int // 0 = Monday … 6 = Sunday
	startHour int
	startMin  int
	durMin    int
	title     string
	location  string
	desc      string
	allDay    bool
}

// weekTemplate is a fixed Mon–Fri workweek: daily standups, a handful of
// meetings of varied length, a lunch and a long focus block, and one all-day
// event. Order here is the external_ref order ("evt-1", "evt-2", …) and is
// deterministic. ~14 events.
var weekTemplate = []eventSpec{
	// Monday
	{day: 0, startHour: 9, startMin: 0, durMin: 15, title: "Daily standup", location: "Zoom"},
	{day: 0, startHour: 11, startMin: 0, durMin: 60, title: "Design review", location: "Room 4 · Bldg A", desc: "Walk through the Q3 dashboard mocks and agree on scope."},
	{day: 0, startHour: 16, startMin: 0, durMin: 30, title: "Sprint review", location: "Room 2"},
	// Tuesday
	{day: 1, startHour: 9, startMin: 0, durMin: 15, title: "Daily standup", location: "Zoom"},
	{day: 1, startHour: 12, startMin: 30, durMin: 60, title: "Lunch with Sam", location: "Cafe Loop"},
	{day: 1, startHour: 14, startMin: 0, durMin: 120, title: "Focus: sync engine", desc: "Heads-down block — no meetings. Ship the reconcile path."},
	// Wednesday
	{day: 2, startHour: 9, startMin: 0, durMin: 15, title: "Daily standup", location: "Zoom"},
	{day: 2, startHour: 10, startMin: 0, durMin: 60, title: "1:1 with Alex", location: "Zoom", desc: "Career check-in and blockers."},
	{day: 2, startHour: 15, startMin: 0, durMin: 60, title: "Planning", location: "Room 2", desc: "Groom the backlog for next sprint."},
	// Thursday
	{day: 3, startHour: 9, startMin: 0, durMin: 15, title: "Daily standup", location: "Zoom"},
	{day: 3, allDay: true, title: "Conference", location: "Convention Center", desc: "Out of office — annual industry conference."},
	// Friday
	{day: 4, startHour: 9, startMin: 0, durMin: 15, title: "Daily standup", location: "Zoom"},
	{day: 4, startHour: 12, startMin: 0, durMin: 60, title: "Team lunch", location: "The Bistro"},
	{day: 4, startHour: 15, startMin: 0, durMin: 45, title: "Retro", location: "Room 3", desc: "What went well, what to change."},
}

// mockEvents renders weekTemplate against the week containing now, in now's
// location. Deterministic: the same now always yields byte-identical output.
// due_time = start; completed_time = end for events already ended.
func mockEvents(now time.Time) []*taskpb.ExternalTask {
	monday := weekMonday(now)
	tasks := make([]*taskpb.ExternalTask, 0, len(weekTemplate))
	for i, s := range weekTemplate {
		day := monday.AddDate(0, 0, s.day)
		var start, end time.Time
		if s.allDay {
			start = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
			end = start.AddDate(0, 0, 1) // all-day spans to the next midnight
		} else {
			start = time.Date(day.Year(), day.Month(), day.Day(), s.startHour, s.startMin, 0, 0, day.Location())
			end = start.Add(time.Duration(s.durMin) * time.Minute)
		}

		data := map[string]any{
			"all_day": s.allDay,
			"start":   start.Format(time.RFC3339),
			"end":     end.Format(time.RFC3339),
		}
		if s.location != "" {
			data["location"] = s.location
		}
		if s.desc != "" {
			data["description"] = s.desc
		}

		t := &taskpb.ExternalTask{
			ExternalRef:  fmt.Sprintf("evt-%d", i+1),
			Title:        s.title,
			DueTime:      timestamppb.New(start),
			ExternalData: mustStruct(data),
		}
		if end.Before(now) {
			t.CompletedTime = timestamppb.New(end)
		}
		tasks = append(tasks, t)
	}
	return tasks
}

// weekMonday returns 00:00 on the Monday of the week containing now, in now's
// location.
func weekMonday(now time.Time) time.Time {
	offset := (int(now.Weekday()) + 6) % 7 // days since Monday (Mon=0 … Sun=6)
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return midnight.AddDate(0, 0, -offset)
}

// mustStruct builds a protobuf Struct from a map of JSON scalars. The inputs
// here are all strings and bools, which structpb never rejects, so a failure
// would be a programmer error — panic rather than thread an impossible error
// through mockEvents' signature.
func mustStruct(m map[string]any) *structpb.Struct {
	s, err := structpb.NewStruct(m)
	if err != nil {
		panic(fmt.Sprintf("gcal: external_data: %v", err))
	}
	return s
}
