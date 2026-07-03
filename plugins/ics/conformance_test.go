package ics_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
	"todoapp/pkg/taskplugin"
	"todoapp/pkg/taskplugin/connectortest"
	"todoapp/plugins/ics"
)

// icsDriver backs the conformance suite with a real .ics file the connector
// re-reads on every snapshot — the closest thing a file-based connector has
// to "mutating the remote".
type icsDriver struct {
	t    *testing.T
	path string
	conn *ics.Connector
	evs  map[string]*pluginv1.RemoteItem // external_id -> desired state
}

func (d *icsDriver) Connector() taskplugin.Connector { return d.conn }

func (d *icsDriver) Put(t *testing.T, item *pluginv1.RemoteItem) {
	t.Helper()
	d.evs[item.GetExternalId()] = item
	d.rewrite(t)
}

func (d *icsDriver) Delete(t *testing.T, externalID string) {
	t.Helper()
	delete(d.evs, externalID)
	d.rewrite(t)
}

func (d *icsDriver) rewrite(t *testing.T) {
	t.Helper()
	var b strings.Builder
	w := func(s string) { b.WriteString(s + "\r\n") }
	w("BEGIN:VCALENDAR")
	w("VERSION:2.0")
	w("PRODID:-//conformance//EN")
	ids := make([]string, 0, len(d.evs))
	for id := range d.evs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for i, id := range ids {
		it := d.evs[id]
		w("BEGIN:VEVENT")
		w("UID:" + id)
		w(fmt.Sprintf("DTSTART:2026070%dT1%d0000Z", (i%5)+1, i%10))
		w("DTSTAMP:20260701T000000Z")
		w("SUMMARY:" + it.GetTitle())
		w("END:VEVENT")
	}
	w("END:VCALENDAR")
	if err := os.WriteFile(d.path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestConformance(t *testing.T) {
	connectortest.RunConformance(t, func(t *testing.T) connectortest.Driver {
		path := filepath.Join(t.TempDir(), "cal.ics")
		d := &icsDriver{
			t:    t,
			path: path,
			conn: &ics.Connector{Now: func() time.Time { return time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC) }},
			evs:  map[string]*pluginv1.RemoteItem{},
		}
		d.rewrite(t)
		cfg, err := structpb.NewStruct(map[string]any{"url": "file://" + path})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.conn.Configure(context.Background(), "cal@conf", cfg); err != nil {
			t.Fatalf("configure: %v", err)
		}
		return d
	})
}
