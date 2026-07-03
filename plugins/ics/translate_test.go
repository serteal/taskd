package ics

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	icspluginv1 "todoapp/gen/icsplugin/v1"
	pluginv1 "todoapp/gen/taskcore/plugin/v1"
	taskcorev1 "todoapp/gen/taskcore/v1"
)

// fixedNow is the reference clock for all golden tests; horizon 60 days
// gives the window [2026-05-04T12:00:00Z, 2026-09-01T12:00:00Z].
var fixedNow = time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func translateFixture(t *testing.T, name string, now time.Time, horizonDays int) ([]*pluginv1.RemoteItem, []string) {
	t.Helper()
	items, warnings, err := Translate(loadFixture(t, name), now, horizonDays)
	if err != nil {
		t.Fatalf("Translate(%s): %v", name, err)
	}
	return items, warnings
}

// row is the flattened, golden-comparable shape of one RemoteItem.
type row struct {
	id     string
	kind   string
	title  string
	state  string
	parent string
	start  string
	end    string
	allDay bool
	rrule  string
}

func rowsOf(t *testing.T, items []*pluginv1.RemoteItem) []row {
	t.Helper()
	rows := make([]row, 0, len(items))
	for _, it := range items {
		ev := decodeEvent(t, it)
		rows = append(rows, row{
			id:     it.ExternalId,
			kind:   it.Kind,
			title:  it.Title,
			state:  it.State,
			parent: it.ParentExternalId,
			start:  ev.Start.AsTime().UTC().Format(time.RFC3339),
			end:    ev.End.AsTime().UTC().Format(time.RFC3339),
			allDay: ev.AllDay,
			rrule:  ev.Rrule,
		})
		// Structural invariants that hold for every item.
		if it.ParentExternalId != "" {
			if it.ParentRelation != taskcorev1.RelationType_RELATION_TYPE_INSTANCE_OF {
				t.Errorf("%s: parent relation = %v, want INSTANCE_OF", it.ExternalId, it.ParentRelation)
			}
			if want := it.ParentExternalId + "/"; !strings.HasPrefix(it.ExternalId, want) {
				t.Errorf("%s: instance id does not extend parent id %q", it.ExternalId, it.ParentExternalId)
			}
		} else if it.ParentRelation != taskcorev1.RelationType_RELATION_TYPE_UNSPECIFIED {
			t.Errorf("%s: parent relation set without parent_external_id", it.ExternalId)
		}
	}
	return rows
}

func decodeEvent(t *testing.T, it *pluginv1.RemoteItem) *icspluginv1.Event {
	t.Helper()
	a := it.Data["event"]
	if a == nil {
		t.Fatalf("%s: no data[\"event\"] payload", it.ExternalId)
	}
	ev := &icspluginv1.Event{}
	if err := a.UnmarshalTo(ev); err != nil {
		t.Fatalf("%s: unmarshal event payload: %v", it.ExternalId, err)
	}
	return ev
}

func requireRows(t *testing.T, got, want []row) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("item count = %d, want %d\ngot: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("item %d:\n got  %+v\n want %+v", i, got[i], want[i])
		}
	}
}

func TestTranslateSimple(t *testing.T) {
	items, warnings := translateFixture(t, "simple.ics", fixedNow, 60)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	requireRows(t, rowsOf(t, items), []row{
		{id: "simple-1@example.com", kind: KindEvent, title: "Dentist appointment", state: "confirmed",
			start: "2026-07-10T09:00:00Z", end: "2026-07-10T10:00:00Z"},
		{id: "simple-2@example.com", kind: KindEvent, title: "Coffee with Sam", state: "tentative",
			start: "2026-08-05T14:00:00Z", end: "2026-08-05T15:00:00Z"},
	})
	// Text unescaping and organizer passthrough on the first event.
	ev := decodeEvent(t, items[0])
	if ev.Location != "123 Main St, Springfield" {
		t.Errorf("location = %q, want unescaped street address", ev.Location)
	}
	if ev.Organizer != "mailto:frontdesk@dentist.example" {
		t.Errorf("organizer = %q, want verbatim mailto address", ev.Organizer)
	}
}

func TestTranslateWeekly(t *testing.T) {
	items, warnings := translateFixture(t, "weekly.ics", fixedNow, 60)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	const uid = "weekly-sync@example.com"
	requireRows(t, rowsOf(t, items), []row{
		{id: uid, kind: KindSeries, title: "Weekly Sync", state: StateSeries,
			start: "2026-07-07T15:00:00Z", end: "2026-07-07T15:30:00Z", rrule: "FREQ=WEEKLY;COUNT=6"},
		{id: uid + "/2026-07-07T15:00:00Z", kind: KindEvent, title: "Weekly Sync", state: "confirmed",
			parent: uid, start: "2026-07-07T15:00:00Z", end: "2026-07-07T15:30:00Z"},
		// RECURRENCE-ID override: renamed AND moved a day later, but the
		// external_id stays keyed by the original occurrence time.
		{id: uid + "/2026-07-14T15:00:00Z", kind: KindEvent, title: "Weekly Sync (moved)", state: "confirmed",
			parent: uid, start: "2026-07-15T16:00:00Z", end: "2026-07-15T16:30:00Z"},
		// 2026-07-21 is EXDATEd out.
		{id: uid + "/2026-07-28T15:00:00Z", kind: KindEvent, title: "Weekly Sync", state: "confirmed",
			parent: uid, start: "2026-07-28T15:00:00Z", end: "2026-07-28T15:30:00Z"},
		{id: uid + "/2026-08-04T15:00:00Z", kind: KindEvent, title: "Weekly Sync", state: "confirmed",
			parent: uid, start: "2026-08-04T15:00:00Z", end: "2026-08-04T15:30:00Z"},
		{id: uid + "/2026-08-11T15:00:00Z", kind: KindEvent, title: "Weekly Sync", state: "confirmed",
			parent: uid, start: "2026-08-11T15:00:00Z", end: "2026-08-11T15:30:00Z"},
	})
	// The series item inherits the base LOCATION.
	if ev := decodeEvent(t, items[0]); ev.Location != "Conf Room B" {
		t.Errorf("series location = %q, want %q", ev.Location, "Conf Room B")
	}
}

func TestTranslateMovedOverrideKeepsIdentity(t *testing.T) {
	items, _ := translateFixture(t, "weekly.ics", fixedNow, 60)
	const wantID = "weekly-sync@example.com/2026-07-14T15:00:00Z"
	for _, it := range items {
		if it.ExternalId == wantID {
			ev := decodeEvent(t, it)
			if got := ev.Start.AsTime().UTC().Format(time.RFC3339); got != "2026-07-15T16:00:00Z" {
				t.Errorf("moved override start = %s, want 2026-07-15T16:00:00Z", got)
			}
			if it.Title != "Weekly Sync (moved)" {
				t.Errorf("moved override title = %q", it.Title)
			}
			return
		}
	}
	t.Fatalf("no item with external_id %s; the moved override lost its original-occurrence identity", wantID)
}

func TestTranslateAllDay(t *testing.T) {
	items, warnings := translateFixture(t, "allday.ics", fixedNow, 60)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	const daily = "allday-daily@example.com"
	want := []row{
		// Multi-day all-day event: DTEND is exclusive per RFC 5545.
		{id: "allday-conf@example.com", kind: KindEvent, title: "Team Offsite", state: "confirmed",
			start: "2026-07-20T00:00:00Z", end: "2026-07-22T00:00:00Z", allDay: true},
		{id: daily, kind: KindSeries, title: "Focus Week", state: StateSeries,
			start: "2026-07-01T00:00:00Z", end: "2026-07-02T00:00:00Z", allDay: true, rrule: "FREQ=DAILY;COUNT=5"},
	}
	for d := 1; d <= 5; d++ {
		want = append(want, row{
			id: fmt.Sprintf("%s/2026-07-%02dT00:00:00Z", daily, d), kind: KindEvent,
			title: "Focus Week", state: "confirmed", parent: daily,
			start:  fmt.Sprintf("2026-07-%02dT00:00:00Z", d),
			end:    fmt.Sprintf("2026-07-%02dT00:00:00Z", d+1),
			allDay: true,
		})
	}
	requireRows(t, rowsOf(t, items), want)
}

func TestTranslateCancelled(t *testing.T) {
	items, warnings := translateFixture(t, "cancelled.ics", fixedNow, 60)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	const uid = "cancel-series@example.com"
	requireRows(t, rowsOf(t, items), []row{
		{id: uid, kind: KindSeries, title: "Monday check-in", state: StateSeries,
			start: "2026-07-06T09:00:00Z", end: "2026-07-06T09:30:00Z", rrule: "FREQ=WEEKLY;COUNT=3"},
		{id: uid + "/2026-07-06T09:00:00Z", kind: KindEvent, title: "Monday check-in", state: "confirmed",
			parent: uid, start: "2026-07-06T09:00:00Z", end: "2026-07-06T09:30:00Z"},
		// Cancelled override is still emitted, with state "cancelled".
		{id: uid + "/2026-07-13T09:00:00Z", kind: KindEvent, title: "Monday check-in", state: "cancelled",
			parent: uid, start: "2026-07-13T09:00:00Z", end: "2026-07-13T09:30:00Z"},
		{id: uid + "/2026-07-20T09:00:00Z", kind: KindEvent, title: "Monday check-in", state: "confirmed",
			parent: uid, start: "2026-07-20T09:00:00Z", end: "2026-07-20T09:30:00Z"},
		// Cancelled single event is still emitted too.
		{id: "cancelled-1@example.com", kind: KindEvent, title: "Vendor demo", state: "cancelled",
			start: "2026-07-15T10:00:00Z", end: "2026-07-15T11:00:00Z"},
	})
}

func TestTranslateTZ(t *testing.T) {
	items, warnings := translateFixture(t, "tz.ics", fixedNow, 60)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	const uid = "tz-weekly@example.com"
	want := []row{
		// TZID single event: 09:30 America/New_York (EDT, -04) = 13:30Z.
		{id: "tz-single@example.com", kind: KindEvent, title: "NY breakfast", state: "confirmed",
			start: "2026-07-10T13:30:00Z", end: "2026-07-10T14:15:00Z"},
		// Series anchored 2026-03-02 10:00 Europe/Berlin: CET (+01) = 09:00Z.
		{id: uid, kind: KindSeries, title: "Berlin standup", state: StateSeries,
			start: "2026-03-02T09:00:00Z", end: "2026-03-02T10:00:00Z", rrule: "FREQ=WEEKLY;COUNT=20"},
	}
	// The series crosses the 2026-03-29 EU spring-forward: in-horizon
	// occurrences keep 10:00 Berlin wall time, which is now 08:00Z (CEST).
	for _, d := range []string{
		"2026-05-11", "2026-05-18", "2026-05-25",
		"2026-06-01", "2026-06-08", "2026-06-15", "2026-06-22", "2026-06-29",
		"2026-07-06", "2026-07-13",
	} {
		want = append(want, row{
			id: uid + "/" + d + "T08:00:00Z", kind: KindEvent, title: "Berlin standup", state: "confirmed",
			parent: uid, start: d + "T08:00:00Z", end: d + "T09:00:00Z",
		})
	}
	requireRows(t, rowsOf(t, items), want)
}

func TestTranslateBadRRuleSkipsOnlyThatEvent(t *testing.T) {
	items, warnings := translateFixture(t, "bad.ics", fixedNow, 60)
	requireRows(t, rowsOf(t, items), []row{
		{id: "bad-good@example.com", kind: KindEvent, title: "Still standing", state: "confirmed",
			start: "2026-07-12T12:00:00Z", end: "2026-07-12T13:00:00Z"},
	})
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one", warnings)
	}
	if !strings.Contains(warnings[0], "bad-rrule@example.com") || !strings.Contains(warnings[0], "RRULE") {
		t.Errorf("warning %q should name the uid and the RRULE failure", warnings[0])
	}
}

// TestTranslateDeterminism: same inputs → element-wise proto.Equal items in
// the same order, and identical warnings, across repeated runs.
func TestTranslateDeterminism(t *testing.T) {
	fixtures := []string{"simple.ics", "weekly.ics", "allday.ics", "cancelled.ics", "tz.ics", "bad.ics"}
	for _, name := range fixtures {
		payload := loadFixture(t, name)
		a, warnA, err := Translate(payload, fixedNow, 60)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		b, warnB, err := Translate(payload, fixedNow, 60)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(a) != len(b) {
			t.Fatalf("%s: run lengths differ: %d vs %d", name, len(a), len(b))
		}
		for i := range a {
			if a[i].ExternalId != b[i].ExternalId {
				t.Errorf("%s: order differs at %d: %s vs %s", name, i, a[i].ExternalId, b[i].ExternalId)
			}
			if !proto.Equal(a[i], b[i]) {
				t.Errorf("%s: item %d (%s) differs between runs", name, i, a[i].ExternalId)
			}
		}
		if len(warnA) != len(warnB) {
			t.Fatalf("%s: warning counts differ", name)
		}
		for i := range warnA {
			if warnA[i] != warnB[i] {
				t.Errorf("%s: warning %d differs: %q vs %q", name, i, warnA[i], warnB[i])
			}
		}
		// Items must arrive sorted by external_id.
		for i := 1; i < len(a); i++ {
			if a[i-1].ExternalId >= a[i].ExternalId {
				t.Errorf("%s: items not sorted: %s >= %s", name, a[i-1].ExternalId, a[i].ExternalId)
			}
		}
	}
}

// TestTranslateIdentityStability: retranslating with `now` advanced keeps
// external_ids for every item that is still inside the window, and those
// items are byte-for-byte identical.
func TestTranslateIdentityStability(t *testing.T) {
	payload := loadFixture(t, "weekly.ics")
	base, _, err := Translate(payload, fixedNow, 60)
	if err != nil {
		t.Fatal(err)
	}
	baseByID := map[string]*pluginv1.RemoteItem{}
	for _, it := range base {
		baseByID[it.ExternalId] = it
	}

	t.Run("plus one day: identical set", func(t *testing.T) {
		next, _, err := Translate(payload, fixedNow.AddDate(0, 0, 1), 60)
		if err != nil {
			t.Fatal(err)
		}
		if len(next) != len(base) {
			t.Fatalf("item count changed: %d -> %d", len(base), len(next))
		}
		for i, it := range next {
			if it.ExternalId != base[i].ExternalId {
				t.Errorf("id at %d changed: %s -> %s", i, base[i].ExternalId, it.ExternalId)
			}
			if !proto.Equal(it, base[i]) {
				t.Errorf("%s changed under retranslation", it.ExternalId)
			}
		}
	})

	t.Run("plus seventy days: window shifts, surviving ids exact", func(t *testing.T) {
		next, _, err := Translate(payload, fixedNow.AddDate(0, 0, 70), 60)
		if err != nil {
			t.Fatal(err)
		}
		// Window is now [2026-07-13T12:00Z, 2026-11-10T12:00Z]: the 07-07
		// instance drops out; series + 4 later instances survive.
		wantIDs := []string{
			"weekly-sync@example.com",
			"weekly-sync@example.com/2026-07-14T15:00:00Z",
			"weekly-sync@example.com/2026-07-28T15:00:00Z",
			"weekly-sync@example.com/2026-08-04T15:00:00Z",
			"weekly-sync@example.com/2026-08-11T15:00:00Z",
		}
		if len(next) != len(wantIDs) {
			t.Fatalf("got %d items, want %d", len(next), len(wantIDs))
		}
		for i, it := range next {
			if it.ExternalId != wantIDs[i] {
				t.Fatalf("id %d = %s, want %s", i, it.ExternalId, wantIDs[i])
			}
			if !proto.Equal(it, baseByID[it.ExternalId]) {
				t.Errorf("%s not identical to the original translation", it.ExternalId)
			}
		}
	})
}

func TestTranslateHorizonExclusion(t *testing.T) {
	t.Run("single event outside a narrow horizon is dropped", func(t *testing.T) {
		// Horizon 10: [06-23, 07-13]. simple-1 (07-10) stays,
		// simple-2 (08-05) is dropped.
		items, _ := translateFixture(t, "simple.ics", fixedNow, 10)
		if len(items) != 1 || items[0].ExternalId != "simple-1@example.com" {
			t.Fatalf("items = %v, want only simple-1@example.com", externalIDs(items))
		}
	})
	t.Run("series with no in-horizon occurrence still emits the series item", func(t *testing.T) {
		// A year later every COUNT=6 occurrence is in the past, but the
		// series DTSTART is before the horizon end, so the series survives.
		items, _ := translateFixture(t, "weekly.ics", fixedNow.AddDate(1, 0, 0), 60)
		if len(items) != 1 || items[0].ExternalId != "weekly-sync@example.com" || items[0].Kind != KindSeries {
			t.Fatalf("items = %v, want only the series item", externalIDs(items))
		}
	})
	t.Run("series starting after the horizon end is dropped entirely", func(t *testing.T) {
		items, _ := translateFixture(t, "weekly.ics", fixedNow.AddDate(-1, 0, 0), 60)
		if len(items) != 0 {
			t.Fatalf("items = %v, want none", externalIDs(items))
		}
	})
	t.Run("non-positive horizon falls back to the default", func(t *testing.T) {
		items, _ := translateFixture(t, "simple.ics", fixedNow, 0)
		if len(items) != 2 {
			t.Fatalf("items = %v, want both simple events under the default horizon", externalIDs(items))
		}
	})
}

func TestTranslateMalformedPayload(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
	}{
		{"garbage", "this is not a calendar\r\n"},
		{"empty", ""},
		{"whitespace", "   \r\n  "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := Translate([]byte(tc.payload), fixedNow, 60); err == nil {
				t.Fatal("Translate accepted a malformed payload")
			}
		})
	}
}

func externalIDs(items []*pluginv1.RemoteItem) []string {
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ExternalId
	}
	return ids
}
