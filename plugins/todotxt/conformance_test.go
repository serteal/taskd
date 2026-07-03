package todotxt_test

import (
	"context"
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
	"todoapp/plugins/todotxt"
)

// todotxtDriver backs the conformance suite with a real todo.txt file the
// connector re-reads on every snapshot. Each remote object is one line of the
// form "<title> id:<external_id>", so the id: tag pins the external_id the
// suite expects.
type todotxtDriver struct {
	path  string
	conn  *todotxt.Connector
	items map[string]*pluginv1.RemoteItem // external_id -> desired state
}

func (d *todotxtDriver) Connector() taskplugin.Connector { return d.conn }

func (d *todotxtDriver) Put(t *testing.T, item *pluginv1.RemoteItem) {
	t.Helper()
	d.items[item.GetExternalId()] = item
	d.rewrite(t)
}

func (d *todotxtDriver) Delete(t *testing.T, externalID string) {
	t.Helper()
	delete(d.items, externalID)
	d.rewrite(t)
}

func (d *todotxtDriver) rewrite(t *testing.T) {
	t.Helper()
	ids := make([]string, 0, len(d.items))
	for id := range d.items {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b strings.Builder
	for _, id := range ids {
		b.WriteString(d.items[id].GetTitle() + " id:" + id + "\n")
	}
	if err := os.WriteFile(d.path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestConformance(t *testing.T) {
	connectortest.RunConformance(t, func(t *testing.T) connectortest.Driver {
		path := filepath.Join(t.TempDir(), "todo.txt")
		d := &todotxtDriver{
			path:  path,
			conn:  &todotxt.Connector{Now: func() time.Time { return time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC) }},
			items: map[string]*pluginv1.RemoteItem{},
		}
		d.rewrite(t) // create the (empty) file so Configure's existence check passes
		cfg, err := structpb.NewStruct(map[string]any{"path": path})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.conn.Configure(context.Background(), "todo@conf", cfg); err != nil {
			t.Fatalf("configure: %v", err)
		}
		return d
	})
}
