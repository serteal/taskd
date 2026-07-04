package main

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	taskpb "github.com/serteal/taskd/gen/task"
	"github.com/serteal/taskd/gen/task/taskconnect"
)

// gcalFakeNow is a Wednesday noon (the week's Monday is 2026-07-06), so events
// on Mon/Tue and Wednesday morning fall in the past while the rest are future.
var gcalFakeNow = time.Date(2026, time.July, 8, 12, 0, 0, 0, time.UTC)

func TestMockEventsDeterministic(t *testing.T) {
	a := mockEvents(gcalFakeNow)
	b := mockEvents(gcalFakeNow)
	if len(a) != len(b) {
		t.Fatalf("nondeterministic length: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if !proto.Equal(a[i], b[i]) {
			t.Errorf("task %d differs between runs:\n a=%v\n b=%v", i, a[i], b[i])
		}
	}
}

func TestMockEventsCount(t *testing.T) {
	got := mockEvents(gcalFakeNow)
	if len(got) < 12 || len(got) > 16 {
		t.Fatalf("got %d events, want 12–16", len(got))
	}
}

func TestMockEventsRefsUnique(t *testing.T) {
	got := mockEvents(gcalFakeNow)
	seen := make(map[string]bool, len(got))
	for _, tk := range got {
		if tk.ExternalRef == "" {
			t.Errorf("empty external_ref on %q", tk.Title)
		}
		if seen[tk.ExternalRef] {
			t.Errorf("duplicate external_ref %q", tk.ExternalRef)
		}
		seen[tk.ExternalRef] = true
	}
}

func TestMockEventsExternalDataSchema(t *testing.T) {
	got := mockEvents(gcalFakeNow)
	for _, tk := range got {
		if tk.ExternalData == nil {
			t.Fatalf("%s: nil external_data", tk.ExternalRef)
		}
		d := tk.ExternalData.AsMap()
		for _, key := range []string{"start", "end", "all_day"} {
			if _, ok := d[key]; !ok {
				t.Errorf("%s: external_data missing %q: %v", tk.ExternalRef, key, d)
			}
		}
		if _, ok := d["all_day"].(bool); !ok {
			t.Errorf("%s: all_day not a bool: %T", tk.ExternalRef, d["all_day"])
		}
		start, ok := d["start"].(string)
		if !ok {
			t.Errorf("%s: start not a string", tk.ExternalRef)
			continue
		}
		if _, err := time.Parse(time.RFC3339, start); err != nil {
			t.Errorf("%s: start %q not RFC3339: %v", tk.ExternalRef, start, err)
		}
		// due_time mirrors start.
		if tk.DueTime == nil {
			t.Errorf("%s: due_time unset", tk.ExternalRef)
		} else if got := tk.DueTime.AsTime().Format(time.RFC3339); got != start {
			t.Errorf("%s: due_time %q != start %q", tk.ExternalRef, got, start)
		}
	}
}

func TestMockEventsAllDay(t *testing.T) {
	got := mockEvents(gcalFakeNow)
	allDay := 0
	for _, tk := range got {
		if tk.ExternalData.AsMap()["all_day"] == true {
			allDay++
			if tk.Title == "" {
				t.Errorf("%s: all-day event with empty title", tk.ExternalRef)
			}
		}
	}
	if allDay != 1 {
		t.Errorf("got %d all-day events, want exactly 1", allDay)
	}
}

func TestMockEventsPastCompleted(t *testing.T) {
	got := mockEvents(gcalFakeNow)
	past, future := 0, 0
	for _, tk := range got {
		end, err := time.Parse(time.RFC3339, tk.ExternalData.AsMap()["end"].(string))
		if err != nil {
			t.Fatalf("%s: bad end: %v", tk.ExternalRef, err)
		}
		ended := end.Before(gcalFakeNow)
		if ended {
			past++
			if tk.CompletedTime == nil {
				t.Errorf("%s: ended before now but completed_time unset", tk.ExternalRef)
			} else if !tk.CompletedTime.AsTime().Equal(end) {
				t.Errorf("%s: completed_time %v != end %v", tk.ExternalRef, tk.CompletedTime.AsTime(), end)
			}
		} else {
			future++
			if tk.CompletedTime != nil {
				t.Errorf("%s: not yet ended but completed_time set to %v", tk.ExternalRef, tk.CompletedTime.AsTime())
			}
		}
	}
	if past < 2 {
		t.Errorf("want at least 2 past (completed) events, got %d", past)
	}
	if future < 2 {
		t.Errorf("want at least 2 future events, got %d", future)
	}
}

func TestWeekMonday(t *testing.T) {
	// Every day of the week 2026-07-06 (Mon) … 2026-07-12 (Sun) must resolve
	// to the same Monday midnight.
	wantMon := time.Date(2026, time.July, 6, 0, 0, 0, 0, time.UTC)
	for d := 6; d <= 12; d++ {
		now := time.Date(2026, time.July, d, 15, 30, 0, 0, time.UTC)
		if got := weekMonday(now); !got.Equal(wantMon) {
			t.Errorf("weekMonday(%v) = %v, want %v", now, got, wantMon)
		}
	}
}

// --- syncAccount wiring (mirrors the ics test's fake client) ---

type fakeTaskClient struct {
	taskconnect.TaskServiceClient
	got *taskpb.UpsertExternalTasksRequest
}

func (f *fakeTaskClient) UpsertExternalTasks(_ context.Context, req *connect.Request[taskpb.UpsertExternalTasksRequest]) (*connect.Response[taskpb.UpsertExternalTasksResponse], error) {
	f.got = req.Msg
	return connect.NewResponse(&taskpb.UpsertExternalTasksResponse{}), nil
}

func TestSyncAccount(t *testing.T) {
	fc := &fakeTaskClient{}
	acct := account{Name: "personal", Labels: []string{"calendar"}}
	if err := syncAccount(context.Background(), fc, acct, func() time.Time { return gcalFakeNow }); err != nil {
		t.Fatalf("syncAccount: %v", err)
	}
	if fc.got == nil {
		t.Fatal("UpsertExternalTasks was not called")
	}
	if fc.got.Source != "gcal:personal" {
		t.Errorf("source = %q, want gcal:personal", fc.got.Source)
	}
	if !fc.got.FullSnapshot {
		t.Error("full_snapshot = false, want true")
	}
	if len(fc.got.ApplyLabels) != 1 || fc.got.ApplyLabels[0] != "calendar" {
		t.Errorf("apply_labels = %v, want [calendar]", fc.got.ApplyLabels)
	}
	if len(fc.got.Tasks) == 0 {
		t.Error("no tasks upserted")
	}
}

func TestParseConfig(t *testing.T) {
	cfg, err := parseConfig([]byte(`
interval: 5m
accounts:
  - name: personal
    labels: [calendar]
  - name: work
    labels: [calendar, work]
`))
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.Interval != 5*time.Minute {
		t.Errorf("interval = %v, want 5m", cfg.Interval)
	}
	if len(cfg.Accounts) != 2 || cfg.Accounts[0].Name != "personal" {
		t.Fatalf("accounts = %+v", cfg.Accounts)
	}

	// Empty config → one default account, default interval.
	def, err := parseConfig([]byte(""))
	if err != nil {
		t.Fatalf("parseConfig empty: %v", err)
	}
	if def.Interval != 15*time.Minute {
		t.Errorf("default interval = %v, want 15m", def.Interval)
	}
	if len(def.Accounts) != 1 || def.Accounts[0].Name != "personal" {
		t.Errorf("default accounts = %+v, want one 'personal'", def.Accounts)
	}

	bad := []string{
		"accounts:\n  - labels: [x]",                 // missing name
		"accounts:\n  - name: a\n  - name: a",        // duplicate name
		"interval: nonsense\naccounts:\n  - name: a", // bad interval
		"interval: -5m\naccounts:\n  - name: a",      // non-positive interval
	}
	for _, in := range bad {
		if _, err := parseConfig([]byte(in)); err == nil {
			t.Errorf("parseConfig(%q) succeeded, want error", in)
		}
	}
}
