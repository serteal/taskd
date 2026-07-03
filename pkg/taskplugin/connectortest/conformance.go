package connectortest

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
	"todoapp/pkg/taskplugin"
)

// Driver lets the conformance suite manipulate the remote behind a real
// connector: a file-backed connector rewrites its file, an API-backed one
// calls its sandbox, the Fake just mutates memory. The driver must make the
// given item's external_id be the id the connector reports for that object —
// that mapping is what the suite exercises.
type Driver interface {
	// Connector returns the connector under test, already Configure-d.
	Connector() taskplugin.Connector
	// Put creates or updates one remote object.
	Put(t *testing.T, item *pluginv1.RemoteItem)
	// Delete removes one remote object.
	Delete(t *testing.T, externalID string)
}

// snapshotTimeout bounds one Snapshot call in the suite.
const snapshotTimeout = 30 * time.Second

// RunConformance runs the scenario suite every connector must pass: the
// identity and stability contract the core's snapshot diffing depends on.
// setup is called once per scenario and must return a Driver over a fresh,
// empty remote. Scenario names are kept short on purpose: drivers often use
// t.TempDir for sockets, and macOS caps unix socket paths at 104 bytes.
func RunConformance(t *testing.T, setup func(t *testing.T) Driver) {
	t.Run("empty", func(t *testing.T) {
		d := setup(t)
		if items := snapshot(t, d.Connector()); len(items) != 0 {
			t.Fatalf("empty remote: snapshot has %d items, want 0", len(items))
		}
	})

	t.Run("put", func(t *testing.T) {
		d := setup(t)
		d.Put(t, item("a", "Alpha"))
		d.Put(t, item("b", "Beta"))
		got := byID(t, snapshot(t, d.Connector()))
		if len(got) != 2 {
			t.Fatalf("snapshot has %d items, want 2", len(got))
		}
		for _, id := range []string{"a", "b"} {
			it, ok := got[id]
			if !ok {
				t.Fatalf("snapshot is missing external_id %q", id)
			}
			if it.GetExternalId() == "" || it.GetKind() == "" || it.GetTitle() == "" {
				t.Errorf("item %q must have non-empty external_id/kind/title, got %v", id, it)
			}
		}
	})

	t.Run("stable", func(t *testing.T) {
		// Two snapshots of an unchanged remote must be value-identical:
		// same external_ids, and each RemoteItem serializes to the same
		// bytes. The core's no-op suppression diffs snapshots by value —
		// a connector that jitters (timestamps, random ordering inside
		// items) makes every poll look like a change.
		d := setup(t)
		d.Put(t, item("a", "Alpha"))
		d.Put(t, item("b", "Beta"))
		first := byID(t, snapshot(t, d.Connector()))
		second := byID(t, snapshot(t, d.Connector()))
		if len(first) != len(second) {
			t.Fatalf("snapshot sizes differ: %d then %d", len(first), len(second))
		}
		for id, a := range first {
			b, ok := second[id]
			if !ok {
				t.Fatalf("external_id %q in first snapshot but not second", id)
			}
			ab, bb := marshalDet(t, a), marshalDet(t, b)
			if string(ab) != string(bb) {
				t.Errorf("item %q not byte-stable across snapshots:\n first: %v\nsecond: %v", id, a, b)
			}
		}
	})

	t.Run("edit", func(t *testing.T) {
		d := setup(t)
		d.Put(t, item("a", "Alpha"))
		d.Put(t, item("b", "Beta"))
		before := byID(t, snapshot(t, d.Connector()))

		d.Put(t, item("a", "Alpha v2"))
		after := byID(t, snapshot(t, d.Connector()))
		if len(after) != len(before) {
			t.Fatalf("edit changed snapshot size: %d then %d", len(before), len(after))
		}
		it, ok := after["a"]
		if !ok {
			t.Fatal("external_id \"a\" vanished after an edit; identity must survive edits")
		}
		if it.GetTitle() != "Alpha v2" {
			t.Errorf("edited title = %q, want %q", it.GetTitle(), "Alpha v2")
		}
		if _, ok := after["b"]; !ok {
			t.Error("untouched item \"b\" vanished after editing \"a\"")
		}
	})

	t.Run("delete", func(t *testing.T) {
		d := setup(t)
		d.Put(t, item("a", "Alpha"))
		d.Put(t, item("b", "Beta"))
		d.Delete(t, "a")
		got := byID(t, snapshot(t, d.Connector()))
		if _, ok := got["a"]; ok {
			t.Error("deleted item \"a\" still enumerated")
		}
		if _, ok := got["b"]; !ok {
			t.Error("item \"b\" vanished when \"a\" was deleted")
		}
	})

	t.Run("readd", func(t *testing.T) {
		d := setup(t)
		d.Put(t, item("a", "Alpha"))
		d.Delete(t, "a")
		if got := byID(t, snapshot(t, d.Connector())); len(got) != 0 {
			t.Fatalf("after delete: %d items, want 0", len(got))
		}
		d.Put(t, item("a", "Alpha again"))
		got := byID(t, snapshot(t, d.Connector()))
		it, ok := got["a"]
		if !ok {
			t.Fatal("re-put item \"a\" not enumerated; reappearance must work")
		}
		if it.GetTitle() != "Alpha again" {
			t.Errorf("re-put title = %q, want %q", it.GetTitle(), "Alpha again")
		}
	})

	t.Run("parents", func(t *testing.T) {
		d := setup(t)
		d.Put(t, item("p", "Parent"))
		child := item("c", "Child")
		child.ParentExternalId = "p"
		d.Put(t, child)

		items := snapshot(t, d.Connector())
		ids := make(map[string]bool, len(items))
		for _, it := range items {
			ids[it.GetExternalId()] = true
		}
		for _, it := range items {
			if p := it.GetParentExternalId(); p != "" && !ids[p] {
				t.Errorf("item %q has parent_external_id %q, which is not in the same snapshot (dangling parent)", it.GetExternalId(), p)
			}
		}
		if !ids["c"] || !ids["p"] {
			t.Errorf("snapshot missing parent/child pair, got ids %v", ids)
		}
	})

	t.Run("unique", func(t *testing.T) {
		d := setup(t)
		d.Put(t, item("a", "Alpha"))
		d.Put(t, item("b", "Beta"))
		d.Put(t, item("c", "Gamma"))
		items := snapshot(t, d.Connector())
		seen := make(map[string]int)
		for _, it := range items {
			seen[it.GetExternalId()]++
		}
		for id, n := range seen {
			if n > 1 {
				t.Errorf("external_id %q enumerated %d times in one snapshot, want 1", id, n)
			}
		}
	})
}

// item builds the suite's canonical test object.
func item(id, title string) *pluginv1.RemoteItem {
	return &pluginv1.RemoteItem{
		ExternalId: id,
		Kind:       "conformance.item",
		Title:      title,
	}
}

// snapshot collects one full Snapshot from the connector.
func snapshot(t *testing.T, c taskplugin.Connector) []*pluginv1.RemoteItem {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout)
	defer cancel()
	var items []*pluginv1.RemoteItem
	err := c.Snapshot(ctx, func(it *pluginv1.RemoteItem) error {
		items = append(items, it)
		return nil
	})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	return items
}

// byID indexes a snapshot by external_id, failing on duplicates so every
// scenario implicitly holds the uniqueness invariant.
func byID(t *testing.T, items []*pluginv1.RemoteItem) map[string]*pluginv1.RemoteItem {
	t.Helper()
	m := make(map[string]*pluginv1.RemoteItem, len(items))
	for _, it := range items {
		id := it.GetExternalId()
		if _, dup := m[id]; dup {
			t.Fatalf("external_id %q enumerated twice in one snapshot", id)
		}
		m[id] = it
	}
	return m
}

// marshalDet serializes deterministically (maps sorted) so byte comparison
// tests value stability, not marshal jitter.
func marshalDet(t *testing.T, m proto.Message) []byte {
	t.Helper()
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
