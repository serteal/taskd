// Package ics translates iCalendar (RFC 5545) payloads into the RemoteItems
// the sync core mirrors (DESIGN.md §6). The connector owns translation and
// identity only: it synthesizes stable external_ids and expands recurrence;
// reconciliation, diffing, and tombstone grace belong to the core.
//
// # Identity (frozen contract — must never change once shipped)
//
//   - Non-recurring VEVENT → one RemoteItem, external_id = UID,
//     kind "calendar.event".
//   - Recurring VEVENT (has an RRULE) → one series item, external_id = UID,
//     kind "calendar.series", state "series", PLUS one instance item per
//     occurrence inside the horizon:
//     external_id = UID + "/" + <occurrence start in RFC3339 UTC>,
//     kind "calendar.event", parent_external_id = UID,
//     parent_relation INSTANCE_OF.
//   - The occurrence time in an instance external_id is always the ORIGINAL
//     occurrence time from recurrence expansion. A RECURRENCE-ID override
//     that moves an occurrence changes the instance's start, never its
//     external_id — identity survives a moved instance.
//
// external_id grammar:
//
//	series / single event:  <UID>
//	instance:               <UID> "/" <original occurrence start, RFC3339 UTC>
//	e.g.                    weekly-sync@example.com/2026-07-14T15:00:00Z
//
// # Recurrence
//
// RRULE is expanded with rrule-go anchored at DTSTART (in DTSTART's zone, so
// wall-clock times survive DST transitions). EXDATE values remove generated
// occurrences; RDATE values add occurrences; RECURRENCE-ID overridden VEVENTs
// replace the generated occurrence whose start matches the RECURRENCE-ID.
//
// # Time handling
//
//   - DTSTART/DTEND with TZID → time.LoadLocation(TZID) (IANA database).
//   - UTC "Z" forms are taken as-is; floating times are interpreted as UTC.
//   - All-day (VALUE=DATE) → midnight UTC with all_day = true; DTEND is
//     exclusive per RFC 5545. An all-day event without DTEND spans 24h.
//
// # Documented simplifications / RFC deviations
//
//   - VTIMEZONE blocks are ignored; TZIDs are resolved via the host IANA tz
//     database instead of the embedded definitions.
//   - A TZID that time.LoadLocation cannot resolve makes the whole VEVENT
//     unparseable (skipped with a warning) rather than falling back.
//   - DURATION is not supported: a timed event without DTEND is treated as
//     zero-length; an all-day event without DTEND spans exactly 24 hours.
//   - Recurrence is keyed on RRULE only: an event with RDATE but no RRULE is
//     treated as non-recurring (its RDATEs are ignored). RDATE PERIOD values
//     are not supported and produce a warning.
//   - An override always wins: a RECURRENCE-ID matching no generated
//     occurrence (including one removed by EXDATE) still materializes an
//     instance keyed by that RECURRENCE-ID, if it is inside the horizon.
//   - Only the first RRULE property of a VEVENT is honored; EXRULE is not
//     supported (deprecated by RFC 5545).
//   - A bare 8-digit date value is treated as VALUE=DATE even without the
//     VALUE=DATE parameter (common in the wild).
//   - ORGANIZER is carried verbatim (including any "mailto:" prefix).
//   - Instance horizon membership is decided by the ORIGINAL occurrence
//     start, not the (possibly moved or multi-day) event span; single events
//     are kept if their [start, end] span overlaps the horizon at all.
//   - The series item is emitted for any recurring event whose DTSTART is
//     before the horizon end, even when no occurrence falls inside the
//     horizon; a recurring event whose DTSTART is at/after the horizon end
//     is dropped entirely.
//   - VTODO, VJOURNAL, and VALARM components are ignored.
//   - Duplicate UIDs (two base VEVENTs) keep the first and warn; duplicate
//     overrides for the same RECURRENCE-ID keep the first and warn.
//
// Cancelled events and instances ARE still emitted with state "cancelled":
// distinguishing disappearance from cancellation is the sync engine's
// business, not the connector's.
package ics

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	ical "github.com/arran4/golang-ical"
	"github.com/teambition/rrule-go"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	icspluginv1 "todoapp/gen/icsplugin/v1"
	pluginv1 "todoapp/gen/taskcore/plugin/v1"
	taskcorev1 "todoapp/gen/taskcore/v1"
)

const (
	// KindEvent is the RemoteItem kind for single events and instances.
	KindEvent = "calendar.event"
	// KindSeries is the RemoteItem kind for the hidden recurring-series item.
	KindSeries = "calendar.series"
	// StateSeries is the state carried by every series item.
	StateSeries = "series"
	// stateDefault is the state used when a VEVENT carries no STATUS.
	stateDefault = "confirmed"
)

// Translate turns an ICS payload into RemoteItems within the horizon
// [now - horizonDays*24h, now + horizonDays*24h] (both ends inclusive).
// horizonDays <= 0 falls back to DefaultHorizonDays.
//
// Translate is pure: the same inputs produce byte-identical output. Items
// are sorted by external_id and every map/list inside them is built
// deterministically, because the core's no-op suppression diffs snapshots
// by value.
//
// A payload that cannot be parsed as a calendar at all returns an error.
// A single bad VEVENT (unparseable RRULE, DTSTART, TZID, ...) is skipped
// and reported in warnings; the rest of the feed survives.
func Translate(payload []byte, now time.Time, horizonDays int) ([]*pluginv1.RemoteItem, []string, error) {
	if horizonDays <= 0 {
		horizonDays = DefaultHorizonDays
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		return nil, nil, errors.New("ics translate: empty payload")
	}
	cal, err := ical.ParseCalendar(bytes.NewReader(payload))
	if err != nil {
		return nil, nil, fmt.Errorf("ics translate: parse calendar: %w", err)
	}

	horizon := time.Duration(horizonDays) * 24 * time.Hour
	winStart := now.Add(-horizon)
	winEnd := now.Add(horizon)

	var warnings []string
	warnf := func(format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	}

	// Group VEVENTs by UID. Components carrying RECURRENCE-ID are overrides
	// of individual occurrences; the one without it is the base event.
	type group struct {
		base      *ical.VEvent
		overrides []*ical.VEvent
	}
	groups := map[string]*group{}
	var uids []string
	for _, ev := range cal.Events() {
		uid := strings.TrimSpace(ev.Id())
		if uid == "" {
			warnf("skipping VEVENT without UID")
			continue
		}
		g := groups[uid]
		if g == nil {
			g = &group{}
			groups[uid] = g
			uids = append(uids, uid)
		}
		switch {
		case ev.GetProperty(ical.ComponentPropertyRecurrenceId) != nil:
			g.overrides = append(g.overrides, ev)
		case g.base != nil:
			warnf("uid %s: duplicate VEVENT without RECURRENCE-ID; keeping the first", uid)
		default:
			g.base = ev
		}
	}
	// Process in sorted-UID order so warnings are deterministic too.
	sort.Strings(uids)

	var items []*pluginv1.RemoteItem
	for _, uid := range uids {
		g := groups[uid]
		got, err := translateGroup(uid, g.base, g.overrides, winStart, winEnd, warnf)
		if err != nil {
			warnf("uid %s: %v; skipped", uid, err)
			continue
		}
		items = append(items, got...)
	}

	sort.Slice(items, func(i, j int) bool { return items[i].ExternalId < items[j].ExternalId })
	return items, warnings, nil
}

// translateGroup translates one UID's base VEVENT plus its RECURRENCE-ID
// overrides. A returned error means the whole UID is skipped (the caller
// records it as a warning).
func translateGroup(uid string, base *ical.VEvent, overrides []*ical.VEvent, winStart, winEnd time.Time, warnf func(string, ...any)) ([]*pluginv1.RemoteItem, error) {
	if base == nil {
		return nil, errors.New("RECURRENCE-ID override(s) without a base VEVENT")
	}
	bf, err := extractFields(base)
	if err != nil {
		return nil, err
	}
	if !bf.hasStart {
		return nil, errors.New("missing DTSTART")
	}

	if bf.rrule == "" {
		// Non-recurring: one item, external_id = UID. Kept if its span
		// overlaps the horizon at all.
		if len(overrides) > 0 {
			warnf("uid %s: RECURRENCE-ID override(s) on a non-recurring event; ignored", uid)
		}
		if bf.effectiveEnd().Before(winStart) || bf.start.After(winEnd) {
			return nil, nil // wholly outside the horizon
		}
		item, err := singleItem(uid, bf)
		if err != nil {
			return nil, err
		}
		return []*pluginv1.RemoteItem{item}, nil
	}

	// Recurring. A series whose DTSTART is at/after the horizon end is
	// dropped entirely; otherwise the series item is always emitted, even
	// with zero in-horizon occurrences (simplification, see package doc).
	if !bf.start.Before(winEnd) {
		return nil, nil
	}
	opt, err := rrule.StrToROption(bf.rrule)
	if err != nil {
		return nil, fmt.Errorf("unparseable RRULE %q: %v", bf.rrule, err)
	}
	opt.Dtstart = bf.start // anchor at DTSTART, in DTSTART's zone (DST-safe)
	rule, err := rrule.NewRRule(*opt)
	if err != nil {
		return nil, fmt.Errorf("invalid RRULE %q: %v", bf.rrule, err)
	}

	// Occurrence set keyed by the UTC instant of the ORIGINAL start.
	occ := map[int64]time.Time{}
	for _, t := range rule.Between(winStart, winEnd, true) {
		occ[t.Unix()] = t
	}
	// RDATE adds occurrences (only those inside the horizon).
	for _, p := range base.GetProperties(ical.ComponentPropertyRdate) {
		for _, v := range splitList(p.Value) {
			if strings.Contains(v, "/") {
				warnf("uid %s: RDATE PERIOD value %q not supported; ignored", uid, v)
				continue
			}
			t, _, perr := parseICalTime(p.ICalParameters, v)
			if perr != nil {
				warnf("uid %s: bad RDATE value %q: %v; ignored", uid, v, perr)
				continue
			}
			if !t.Before(winStart) && !t.After(winEnd) {
				occ[t.Unix()] = t
			}
		}
	}
	// EXDATE removes occurrences.
	for _, p := range base.GetProperties(ical.ComponentPropertyExdate) {
		for _, v := range splitList(p.Value) {
			t, _, perr := parseICalTime(p.ICalParameters, v)
			if perr != nil {
				warnf("uid %s: bad EXDATE value %q: %v; ignored", uid, v, perr)
				continue
			}
			delete(occ, t.Unix())
		}
	}
	// Index overrides by their ORIGINAL occurrence time (RECURRENCE-ID).
	// An override materializes its occurrence even if the rule didn't
	// generate it (or EXDATE removed it) — overrides always win.
	ovr := map[int64]*ical.VEvent{}
	for _, o := range overrides {
		rp := o.GetProperty(ical.ComponentPropertyRecurrenceId)
		t, _, perr := parseICalTime(rp.ICalParameters, rp.Value)
		if perr != nil {
			warnf("uid %s: bad RECURRENCE-ID %q: %v; override ignored", uid, rp.Value, perr)
			continue
		}
		key := t.Unix()
		if _, dup := ovr[key]; dup {
			warnf("uid %s: duplicate override for %s; keeping the first", uid, formatUTC(t))
			continue
		}
		ovr[key] = o
		if _, ok := occ[key]; !ok && !t.Before(winStart) && !t.After(winEnd) {
			occ[key] = t
		}
	}

	series, err := seriesItem(uid, bf)
	if err != nil {
		return nil, err
	}
	items := []*pluginv1.RemoteItem{series}

	keys := make([]int64, 0, len(occ))
	for k := range occ {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, k := range keys {
		item, err := instanceItem(uid, occ[k], bf, ovr[k])
		if err != nil {
			warnf("uid %s: occurrence %s: %v; skipped", uid, formatUTC(occ[k]), err)
			continue
		}
		items = append(items, item)
	}
	return items, nil
}

// eventFields is one VEVENT's translated properties.
type eventFields struct {
	summary   string
	location  string
	organizer string
	status    string
	rrule     string
	start     time.Time
	end       time.Time
	hasStart  bool
	hasEnd    bool
	allDay    bool
}

// effectiveEnd is DTEND when present; otherwise start+24h for all-day
// events (DTEND is exclusive per RFC 5545) and start itself for timed
// events (DURATION unsupported — see package doc).
func (f eventFields) effectiveEnd() time.Time {
	if f.hasEnd {
		return f.end
	}
	if f.allDay {
		return f.start.Add(24 * time.Hour)
	}
	return f.start
}

// duration is the span applied to generated occurrences.
func (f eventFields) duration() time.Duration {
	return f.effectiveEnd().Sub(f.start)
}

func extractFields(ev *ical.VEvent) (eventFields, error) {
	var f eventFields
	f.summary = propValue(ev, ical.ComponentPropertySummary)
	f.location = propValue(ev, ical.ComponentPropertyLocation)
	f.organizer = propValue(ev, ical.ComponentPropertyOrganizer)
	f.status = propValue(ev, ical.ComponentPropertyStatus)
	if p := ev.GetProperty(ical.ComponentPropertyRrule); p != nil {
		f.rrule = strings.TrimSpace(p.Value)
	}
	if p := ev.GetProperty(ical.ComponentPropertyDtStart); p != nil {
		t, allDay, err := parseICalTime(p.ICalParameters, p.Value)
		if err != nil {
			return f, fmt.Errorf("DTSTART: %v", err)
		}
		f.start, f.allDay, f.hasStart = t, allDay, true
	}
	if p := ev.GetProperty(ical.ComponentPropertyDtEnd); p != nil {
		t, _, err := parseICalTime(p.ICalParameters, p.Value)
		if err != nil {
			return f, fmt.Errorf("DTEND: %v", err)
		}
		f.end, f.hasEnd = t, true
	}
	return f, nil
}

// singleItem builds the RemoteItem for a non-recurring VEVENT.
func singleItem(uid string, f eventFields) (*pluginv1.RemoteItem, error) {
	data, err := eventData(f.start, f.effectiveEnd(), f.allDay, f.location, f.organizer, "")
	if err != nil {
		return nil, err
	}
	return &pluginv1.RemoteItem{
		ExternalId: uid,
		Kind:       KindEvent,
		Title:      f.summary,
		State:      normalizeStatus(f.status),
		Data:       data,
	}, nil
}

// seriesItem builds the hidden series RemoteItem for a recurring VEVENT.
// It carries the raw RRULE and the series' own SUMMARY/LOCATION.
func seriesItem(uid string, f eventFields) (*pluginv1.RemoteItem, error) {
	data, err := eventData(f.start, f.effectiveEnd(), f.allDay, f.location, f.organizer, f.rrule)
	if err != nil {
		return nil, err
	}
	return &pluginv1.RemoteItem{
		ExternalId: uid,
		Kind:       KindSeries,
		Title:      f.summary,
		State:      StateSeries,
		Data:       data,
	}, nil
}

// instanceItem builds one occurrence's RemoteItem. orig is the ORIGINAL
// occurrence start from expansion (or the RECURRENCE-ID); it keys the
// external_id even when an override moves the instance. Override fields win
// per property; anything the override omits falls back to the base event.
func instanceItem(uid string, orig time.Time, base eventFields, override *ical.VEvent) (*pluginv1.RemoteItem, error) {
	start := orig
	end := orig.Add(base.duration())
	allDay := base.allDay
	summary := base.summary
	status := base.status
	location := base.location
	organizer := base.organizer

	if override != nil {
		of, err := extractFields(override)
		if err != nil {
			return nil, err
		}
		if of.hasStart {
			start, allDay = of.start, of.allDay
			end = of.start.Add(base.duration())
		}
		if of.hasEnd {
			end = of.end
		}
		if of.summary != "" {
			summary = of.summary
		}
		if of.status != "" {
			status = of.status
		}
		if of.location != "" {
			location = of.location
		}
		if of.organizer != "" {
			organizer = of.organizer
		}
	}

	data, err := eventData(start, end, allDay, location, organizer, "")
	if err != nil {
		return nil, err
	}
	return &pluginv1.RemoteItem{
		ExternalId:       uid + "/" + formatUTC(orig),
		Kind:             KindEvent,
		Title:            summary,
		State:            normalizeStatus(status),
		Data:             data,
		ParentExternalId: uid,
		ParentRelation:   taskcorev1.RelationType_RELATION_TYPE_INSTANCE_OF,
	}, nil
}

// eventData packs the typed icsplugin.v1.Event payload under data["event"].
func eventData(start, end time.Time, allDay bool, location, organizer, rrule string) (map[string]*anypb.Any, error) {
	ev := &icspluginv1.Event{
		Start:     timestamppb.New(start),
		End:       timestamppb.New(end),
		AllDay:    allDay,
		Location:  location,
		Organizer: organizer,
		Rrule:     rrule,
	}
	a, err := anypb.New(ev)
	if err != nil {
		return nil, fmt.Errorf("encode event payload: %v", err)
	}
	return map[string]*anypb.Any{"event": a}, nil
}

// parseICalTime parses one DATE or DATE-TIME value with its property
// parameters:
//
//   - VALUE=DATE (or a bare 8-digit value) → midnight UTC, allDay = true
//   - trailing "Z" → UTC as written
//   - TZID=<zone> → time.LoadLocation(zone); an unknown zone is an error
//   - floating (no zone info) → interpreted as UTC
func parseICalTime(params map[string][]string, value string) (t time.Time, allDay bool, err error) {
	value = strings.TrimSpace(value)
	isDate := false
	if vs := paramValues(params, string(ical.ParameterValue)); len(vs) > 0 {
		isDate = strings.EqualFold(vs[0], "DATE")
	} else if len(value) == 8 {
		isDate = true // bare DATE without VALUE=DATE (lenient)
	}
	if isDate {
		t, err = time.ParseInLocation("20060102", value, time.UTC)
		if err != nil {
			return time.Time{}, false, fmt.Errorf("bad DATE value %q", value)
		}
		return t, true, nil
	}
	if strings.HasSuffix(value, "Z") {
		t, err = time.ParseInLocation("20060102T150405Z", value, time.UTC)
		if err != nil {
			return time.Time{}, false, fmt.Errorf("bad UTC DATE-TIME value %q", value)
		}
		return t, false, nil
	}
	loc := time.UTC // floating times are interpreted as UTC
	if tz := paramValues(params, string(ical.ParameterTzid)); len(tz) > 0 && tz[0] != "" {
		loc, err = time.LoadLocation(tz[0])
		if err != nil {
			return time.Time{}, false, fmt.Errorf("unknown TZID %q", tz[0])
		}
	}
	t, err = time.ParseInLocation("20060102T150405", value, loc)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("bad DATE-TIME value %q", value)
	}
	return t, false, nil
}

// paramValues looks up a property parameter case-insensitively.
func paramValues(params map[string][]string, key string) []string {
	if v, ok := params[key]; ok {
		return v
	}
	for k, v := range params {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return nil
}

func propValue(ev *ical.VEvent, p ical.ComponentProperty) string {
	if prop := ev.GetProperty(p); prop != nil {
		return prop.Value
	}
	return ""
}

// normalizeStatus lowercases an ICS STATUS ("CONFIRMED", "TENTATIVE",
// "CANCELLED"); absent status means "confirmed".
func normalizeStatus(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return stateDefault
	}
	return s
}

// splitList splits a comma-separated multi-value property value (EXDATE,
// RDATE). DATE/DATE-TIME values cannot contain escaped commas.
func splitList(v string) []string {
	return strings.Split(v, ",")
}

// formatUTC renders the RFC3339 UTC form used in instance external_ids.
func formatUTC(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
