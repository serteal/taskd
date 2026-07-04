package syncer

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	taskpb "todoapp/gen/task"
	"todoapp/gen/task/taskconnect"
)

// icsFakeNow anchors every test; fixtures are written relative to it.
// Window: [2026-07-03T12:00Z, 2026-08-03T12:00Z].
var icsFakeNow = time.Date(2026, time.July, 4, 12, 0, 0, 0, time.UTC)

type fakeTaskClient struct {
	taskconnect.TaskServiceClient
	got *taskpb.UpsertExternalTasksRequest
}

func (f *fakeTaskClient) UpsertExternalTasks(_ context.Context, req *connect.Request[taskpb.UpsertExternalTasksRequest]) (*connect.Response[taskpb.UpsertExternalTasksResponse], error) {
	f.got = req.Msg
	return connect.NewResponse(&taskpb.UpsertExternalTasksResponse{}), nil
}

// icsFile writes an .ics fixture; fixture literals use \n for readability and
// are converted to the CRLF endings the format requires.
func icsFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cal.ics")
	data := strings.ReplaceAll(strings.TrimSpace(body), "\n", "\r\n") + "\r\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// icsSync builds the syncer through New (proving registry wiring), pins the
// clock, syncs the fixture, and returns the captured upsert request.
func icsSync(t *testing.T, cfg Config, body string) *taskpb.UpsertExternalTasksRequest {
	t.Helper()
	cfg.Type = "ics"
	if cfg.Name == "" {
		cfg.Name = "work"
	}
	cfg.Path = icsFile(t, body)
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.(*icsSyncer).now = func() time.Time { return icsFakeNow }
	fc := &fakeTaskClient{}
	if err := s.Sync(context.Background(), fc); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if fc.got == nil {
		t.Fatal("UpsertExternalTasks was not called")
	}
	return fc.got
}

func refs(req *taskpb.UpsertExternalTasksRequest) []string {
	var out []string
	for _, tk := range req.Tasks {
		out = append(out, tk.ExternalRef)
	}
	return out
}

func TestICSUpcomingEvent(t *testing.T) {
	got := icsSync(t, Config{Labels: []string{"cal", "work"}}, `
BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//test//EN
BEGIN:VEVENT
UID:ev1@test
SUMMARY:Design review
LOCATION:Room 4
DTSTART:20260705T100000Z
DTEND:20260705T110000Z
END:VEVENT
END:VCALENDAR`)

	if got.Source != "ics:work" {
		t.Errorf("source = %q, want %q", got.Source, "ics:work")
	}
	if !got.FullSnapshot {
		t.Error("full_snapshot = false, want true")
	}
	if want := []string{"cal", "work"}; !slices.Equal(got.ApplyLabels, want) {
		t.Errorf("apply_labels = %v, want %v", got.ApplyLabels, want)
	}
	if len(got.Tasks) != 1 {
		t.Fatalf("got %d tasks, want 1: %v", len(got.Tasks), refs(got))
	}
	tk := got.Tasks[0]
	if tk.ExternalRef != "ev1@test" {
		t.Errorf("external_ref = %q, want %q", tk.ExternalRef, "ev1@test")
	}
	if tk.Title != "Design review" {
		t.Errorf("title = %q, want %q", tk.Title, "Design review")
	}
	wantDue := time.Date(2026, time.July, 5, 10, 0, 0, 0, time.UTC)
	if !tk.DueTime.AsTime().Equal(wantDue) {
		t.Errorf("due_time = %v, want %v", tk.DueTime.AsTime(), wantDue)
	}
	if tk.CompletedTime != nil {
		t.Errorf("completed_time = %v, want unset", tk.CompletedTime.AsTime())
	}
	data := tk.ExternalData.AsMap()
	if data["uid"] != "ev1@test" || data["location"] != "Room 4" {
		t.Errorf("external_data = %v, want uid/location set", data)
	}
	if data["start"] != "2026-07-05T10:00:00Z" || data["end"] != "2026-07-05T11:00:00Z" {
		t.Errorf("external_data times = %v/%v", data["start"], data["end"])
	}
	if data["all_day"] != false {
		t.Errorf("all_day = %v, want false", data["all_day"])
	}
	if _, ok := data["description"]; ok {
		t.Error("empty description should be omitted from external_data")
	}
}

func TestICSPastEventCompleted(t *testing.T) {
	got := icsSync(t, Config{}, `
BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//test//EN
BEGIN:VEVENT
UID:past@test
SUMMARY:Morning sync
DTSTART:20260704T080000Z
DTEND:20260704T090000Z
END:VEVENT
END:VCALENDAR`)

	if len(got.Tasks) != 1 {
		t.Fatalf("got %d tasks, want 1: %v", len(got.Tasks), refs(got))
	}
	tk := got.Tasks[0]
	if tk.CompletedTime == nil {
		t.Fatal("completed_time unset, want DTEND")
	}
	wantEnd := time.Date(2026, time.July, 4, 9, 0, 0, 0, time.UTC)
	if !tk.CompletedTime.AsTime().Equal(wantEnd) {
		t.Errorf("completed_time = %v, want %v", tk.CompletedTime.AsTime(), wantEnd)
	}
}

func TestICSWindowFiltering(t *testing.T) {
	got := icsSync(t, Config{}, `
BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//test//EN
BEGIN:VEVENT
UID:far-future@test
SUMMARY:Way out
DTSTART:20260901T100000Z
END:VEVENT
BEGIN:VEVENT
UID:stale@test
SUMMARY:Too old
DTSTART:20260701T100000Z
END:VEVENT
BEGIN:VEVENT
UID:in-window@test
DTSTART:20260710T100000Z
END:VEVENT
END:VCALENDAR`)

	if want := []string{"in-window@test"}; !slices.Equal(refs(got), want) {
		t.Fatalf("refs = %v, want %v", refs(got), want)
	}
	if tk := got.Tasks[0]; tk.Title != "(untitled event)" {
		t.Errorf("title = %q, want fallback %q", tk.Title, "(untitled event)")
	}
}

func TestICSRecurrenceExpansion(t *testing.T) {
	got := icsSync(t, Config{}, `
BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//test//EN
BEGIN:VEVENT
UID:standup@test
SUMMARY:Standup
DTSTART:20260706T090000Z
DTEND:20260706T091500Z
RRULE:FREQ=WEEKLY;COUNT=10
EXDATE:20260713T090000Z
END:VEVENT
END:VCALENDAR`)

	// Weekly from 7/6 up to the window's end (8/3 12:00Z), minus the
	// EXDATE'd 7/13. Also proves the batch arrives sorted by ref.
	want := []string{
		"standup@test/2026-07-06T09:00:00Z",
		"standup@test/2026-07-20T09:00:00Z",
		"standup@test/2026-07-27T09:00:00Z",
		"standup@test/2026-08-03T09:00:00Z",
	}
	if !slices.Equal(refs(got), want) {
		t.Fatalf("refs = %v, want %v", refs(got), want)
	}
	for _, tk := range got.Tasks {
		if tk.Title != "Standup" {
			t.Errorf("%s: title = %q, want %q", tk.ExternalRef, tk.Title, "Standup")
		}
		if tk.CompletedTime != nil {
			t.Errorf("%s: completed_time set on future occurrence", tk.ExternalRef)
		}
	}
	first := got.Tasks[0]
	wantDue := time.Date(2026, time.July, 6, 9, 0, 0, 0, time.UTC)
	if !first.DueTime.AsTime().Equal(wantDue) {
		t.Errorf("first due_time = %v, want %v", first.DueTime.AsTime(), wantDue)
	}
	// Occurrences keep the series' 15-minute duration.
	if end := first.ExternalData.AsMap()["end"]; end != "2026-07-06T09:15:00Z" {
		t.Errorf("first end = %v, want 2026-07-06T09:15:00Z", end)
	}
}

func TestICSAllDayEvent(t *testing.T) {
	got := icsSync(t, Config{}, `
BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//test//EN
BEGIN:VEVENT
UID:holiday@test
SUMMARY:Company holiday
DTSTART;VALUE=DATE:20260710
END:VEVENT
END:VCALENDAR`)

	if len(got.Tasks) != 1 {
		t.Fatalf("got %d tasks, want 1: %v", len(got.Tasks), refs(got))
	}
	tk := got.Tasks[0]
	if tk.ExternalRef != "holiday@test" {
		t.Errorf("external_ref = %q, want %q", tk.ExternalRef, "holiday@test")
	}
	if data := tk.ExternalData.AsMap(); data["all_day"] != true {
		t.Errorf("all_day = %v, want true", data["all_day"])
	}
	// DATE values parse to midnight in the library's chosen zone (local).
	wantDue := time.Date(2026, time.July, 10, 0, 0, 0, 0, time.Local)
	if !tk.DueTime.AsTime().Equal(wantDue) {
		t.Errorf("due_time = %v, want %v", tk.DueTime.AsTime(), wantDue)
	}
	if tk.CompletedTime != nil {
		t.Error("completed_time set on future all-day event")
	}
}

func TestICSConfigValidation(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"missing name", Config{Type: "ics", URL: "http://example.com/c.ics"}, true},
		{"neither URL nor Path", Config{Type: "ics", Name: "a"}, true},
		{"both URL and Path", Config{Type: "ics", Name: "a", URL: "u", Path: "p"}, true},
		{"URL only", Config{Type: "ics", Name: "a", URL: "http://example.com/c.ics"}, false},
		{"Path only", Config{Type: "ics", Name: "a", Path: "/tmp/c.ics"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := New(tc.cfg) // via the registry: builders["ics"] must be wired
			if tc.wantErr {
				if err == nil {
					t.Fatalf("New(%+v) succeeded, want error", tc.cfg)
				}
				return
			}
			if err != nil {
				t.Fatalf("New(%+v): %v", tc.cfg, err)
			}
			if got := s.Source(); got != "ics:a" {
				t.Errorf("Source() = %q, want %q", got, "ics:a")
			}
		})
	}
}
