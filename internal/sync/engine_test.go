package sync_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/clock"
	"todoapp/internal/feed"
	"todoapp/internal/query"
	"todoapp/internal/store"
	syncengine "todoapp/internal/sync"
)

type fakeSource struct {
	instance string
	items    []*pluginv1.RemoteItem
	err      error
	// resolvable backs Resolve for pinned-refresh tests; ids absent here
	// resolve as gRPC NotFound (a confirmed-gone remote).
	resolvable map[string]*pluginv1.RemoteItem
}

func (f *fakeSource) Instance() string { return f.instance }
func (f *fakeSource) Snapshot(context.Context) ([]*pluginv1.RemoteItem, error) {
	if f.err != nil {
		return nil, f.err
	}
	// Return copies so engine mutations can't leak into the "remote".
	out := make([]*pluginv1.RemoteItem, len(f.items))
	copy(out, f.items)
	return out, nil
}

func (f *fakeSource) Resolve(_ context.Context, ref string) (*pluginv1.RemoteItem, error) {
	if r, ok := f.resolvable[ref]; ok {
		return r, nil
	}
	return nil, status.Errorf(codes.NotFound, "no such object %q", ref)
}

type env struct {
	st   store.Store
	hub  *feed.Hub
	clk  *clock.Fake
	eng  *syncengine.Engine
	src  *fakeSource
	feed *feed.Subscription
}

func newEnv(t *testing.T) *env {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC))
	qeng, err := query.NewEngine(clk.Now)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(store.Options{
		Path:    filepath.Join(t.TempDir(), "t.db"),
		Diff:    feed.Diff,
		Extract: qeng.Extract,
		Now:     clk.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hub := feed.NewHub()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	e := &env{
		st:  st,
		hub: hub,
		clk: clk,
		eng: syncengine.NewEngine(st, hub, clk, clock.NewIDGen(clk, 7), log, 0),
		src: &fakeSource{instance: "cal@t"},
	}
	e.feed = hub.Subscribe(64)
	t.Cleanup(e.feed.Close)
	return e
}

func remote(ext, title string) *pluginv1.RemoteItem {
	return &pluginv1.RemoteItem{
		ExternalId: ext,
		Kind:       "calendar.event",
		Title:      title,
		State:      "confirmed",
	}
}

func (e *env) reconcile(t *testing.T) syncengine.Stats {
	t.Helper()
	stats, err := e.eng.ReconcileOnce(context.Background(), e.src)
	if err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}
	return stats
}

func (e *env) drainEvents() []*taskcorev1.Event {
	var out []*taskcorev1.Event
	for {
		select {
		case ev := <-e.feed.C():
			out = append(out, ev)
		default:
			return out
		}
	}
}

func TestCreateAndNoop(t *testing.T) {
	e := newEnv(t)
	e.src.items = []*pluginv1.RemoteItem{remote("u1", "standup"), remote("u2", "retro")}

	stats := e.reconcile(t)
	if stats.Created != 2 {
		t.Fatalf("created = %d, want 2", stats.Created)
	}
	evs := e.drainEvents()
	if len(evs) != 2 {
		t.Fatalf("events = %d, want 2 CREATED", len(evs))
	}
	for _, ev := range evs {
		if ev.GetType() != taskcorev1.ChangeType_CHANGE_TYPE_CREATED {
			t.Fatalf("event type = %v", ev.GetType())
		}
		if ev.GetCausedBy().GetSource() != taskcorev1.ProvenanceSource_PROVENANCE_SOURCE_SYNC ||
			ev.GetCausedBy().GetRef() != "cal@t" {
			t.Fatalf("provenance = %v, want SYNC/cal@t", ev.GetCausedBy())
		}
		if ev.GetItem().GetTodo() != nil {
			t.Fatal("mirrors arrive un-triaged: no todo layer")
		}
	}

	// Identical second snapshot: zero writes, zero events — no-op
	// suppression at the engine level (this is the echo-silence mechanism).
	e.clk.Advance(time.Minute)
	stats = e.reconcile(t)
	if !(stats == syncengine.Stats{}) {
		t.Fatalf("second identical cycle produced %+v, want all-zero", stats)
	}
	if evs := e.drainEvents(); len(evs) != 0 {
		t.Fatalf("second identical cycle emitted %d events, want 0", len(evs))
	}
}

func TestContentChange(t *testing.T) {
	e := newEnv(t)
	e.src.items = []*pluginv1.RemoteItem{remote("u1", "standup")}
	e.reconcile(t)
	e.drainEvents()

	e.src.items = []*pluginv1.RemoteItem{remote("u1", "standup (moved)")}
	stats := e.reconcile(t)
	if stats.Updated != 1 {
		t.Fatalf("updated = %d, want 1", stats.Updated)
	}
	evs := e.drainEvents()
	if len(evs) != 1 || len(evs[0].GetChanges()) != 1 || evs[0].GetChanges()[0].GetPath() != "mirror.title" {
		t.Fatalf("want exactly one mirror.title change, got %v", evs)
	}
}

func TestMissingGraceStale(t *testing.T) {
	e := newEnv(t)
	e.src.items = []*pluginv1.RemoteItem{remote("u1", "standup")}
	e.reconcile(t)

	// Promote it: the user's todo must survive everything below.
	items, _ := e.st.ListInstanceItems(context.Background(), "cal@t")
	id := items[0].GetId()
	if _, _, err := e.st.MutateItem(context.Background(), id,
		&taskcorev1.Provenance{Source: taskcorev1.ProvenanceSource_PROVENANCE_SOURCE_CLIENT, Ref: "test"},
		func(it *taskcorev1.Item) error {
			it.Todo = &taskcorev1.Todo{Labels: []string{"promoted"}}
			return nil
		}); err != nil {
		t.Fatal(err)
	}
	e.drainEvents()

	// Disappears → missing, not deleted.
	e.src.items = nil
	if stats := e.reconcile(t); stats.Missing != 1 {
		t.Fatalf("missing = %d, want 1", stats.Missing)
	}
	// Still missing within grace: no further writes.
	e.clk.Advance(time.Hour)
	if stats := e.reconcile(t); !(stats == syncengine.Stats{}) {
		t.Fatalf("within-grace cycle produced %+v", stats)
	}
	// Past grace → stale, todo intact, item still present.
	e.clk.Advance(syncengine.DefaultGrace)
	if stats := e.reconcile(t); stats.Stale != 1 {
		t.Fatalf("stale = %d, want 1", stats.Stale)
	}
	got, err := e.st.GetItem(context.Background(), id)
	if err != nil {
		t.Fatalf("item must never be deleted: %v", err)
	}
	if !got.GetMirror().GetStale() || got.GetMirror().GetMissingSince() == nil {
		t.Fatalf("mirror = %v, want stale with missing_since", got.GetMirror())
	}
	if len(got.GetTodo().GetLabels()) != 1 {
		t.Fatal("user todo layer was damaged by tombstoning")
	}

	// Remote comes back → recovered: stale and missing cleared.
	e.src.items = []*pluginv1.RemoteItem{remote("u1", "standup")}
	if stats := e.reconcile(t); stats.Recovered != 1 {
		t.Fatalf("recovered = %d, want 1", stats.Recovered)
	}
	got, _ = e.st.GetItem(context.Background(), id)
	if got.GetMirror().GetStale() || got.GetMirror().GetMissingSince() != nil {
		t.Fatalf("recovery must clear stale/missing, got %v", got.GetMirror())
	}
}

func TestSnapshotErrorTombstonesNothing(t *testing.T) {
	e := newEnv(t)
	e.src.items = []*pluginv1.RemoteItem{remote("u1", "standup")}
	e.reconcile(t)
	e.drainEvents()

	e.src.err = errors.New("remote down")
	if _, err := e.eng.ReconcileOnce(context.Background(), e.src); err == nil {
		t.Fatal("snapshot error must surface")
	}
	items, _ := e.st.ListInstanceItems(context.Background(), "cal@t")
	if items[0].GetMirror().GetMissingSince() != nil {
		t.Fatal("an unreachable remote must never read as an empty remote")
	}
}

func TestParentRelations(t *testing.T) {
	e := newEnv(t)
	series := remote("uid-s", "weekly sync")
	series.Kind = "calendar.series"
	inst := remote("uid-s/2026-07-06T09:00:00Z", "weekly sync")
	inst.ParentExternalId = "uid-s"
	e.src.items = []*pluginv1.RemoteItem{series, inst}

	if stats := e.reconcile(t); stats.Relations != 1 {
		t.Fatalf("relations = %d, want 1", stats.Relations)
	}
	child, err := e.st.GetItemByExternal(context.Background(), "cal@t", "uid-s/2026-07-06T09:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	parent, err := e.st.GetItemByExternal(context.Background(), "cal@t", "uid-s")
	if err != nil {
		t.Fatal(err)
	}
	rels := child.GetRelations()
	if len(rels) != 1 || rels[0].GetType() != taskcorev1.RelationType_RELATION_TYPE_INSTANCE_OF ||
		rels[0].GetTargetId() != parent.GetId() {
		t.Fatalf("relations = %v, want INSTANCE_OF -> %s", rels, parent.GetId())
	}

	// Second cycle: relation not duplicated, zero events.
	e.drainEvents()
	e.clk.Advance(time.Minute)
	if stats := e.reconcile(t); !(stats == syncengine.Stats{}) {
		t.Fatalf("second cycle produced %+v", stats)
	}
}

// TestPinnedRefresh: attached (pinned) mirrors live outside snapshot scope —
// they refresh via Resolve, never get snapshot-disappearance treatment, and
// freeze stale only on a confirmed NotFound.
func TestPinnedRefresh(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	// A pinned item, as LinkItem would create it. Never in any snapshot.
	pinned := &taskcorev1.Item{
		Id: "01PINNED00000000000000000X", Kind: "todotxt.task",
		Mirror: &taskcorev1.Mirror{
			Title: "attached thing", State: "open", Pinned: true,
			Link: &taskcorev1.ExternalLink{ConnectorInstance: "cal@t", ExternalId: "pin-1"},
		},
		MirrorRevision: 1,
	}
	if _, err := e.st.CreateItem(ctx, pinned, &taskcorev1.Provenance{
		Source: taskcorev1.ProvenanceSource_PROVENANCE_SOURCE_SYNC, Ref: "cal@t",
	}); err != nil {
		t.Fatal(err)
	}
	e.src.resolvable = map[string]*pluginv1.RemoteItem{
		"pin-1": {ExternalId: "pin-1", Kind: "todotxt.task", Title: "attached thing (renamed)", State: "open"},
	}

	// Snapshot is empty, yet the pinned item is refreshed, not tombstoned.
	if stats := e.reconcile(t); stats.Updated != 1 || stats.Missing != 0 {
		t.Fatalf("stats = %+v, want 1 updated, 0 missing", stats)
	}
	got, _ := e.st.GetItem(ctx, pinned.GetId())
	if got.GetMirror().GetTitle() != "attached thing (renamed)" || !got.GetMirror().GetPinned() {
		t.Fatalf("pinned refresh failed: %v", got.GetMirror())
	}

	// Remote confirmed gone → stale immediately (no grace ambiguity: a
	// NotFound is an answer, not an absence).
	delete(e.src.resolvable, "pin-1")
	if stats := e.reconcile(t); stats.Stale != 1 {
		t.Fatalf("stats = %+v, want 1 stale", stats)
	}
	got, _ = e.st.GetItem(ctx, pinned.GetId())
	if !got.GetMirror().GetStale() {
		t.Fatal("pinned item with NotFound remote must freeze stale")
	}
}

func TestConnectorBugsAreSkippedLoudly(t *testing.T) {
	e := newEnv(t)
	dupA, dupB := remote("dup", "a"), remote("dup", "b")
	anon := remote("", "no id")
	orphan := remote("child", "orphan")
	orphan.ParentExternalId = "ghost-parent"
	e.src.items = []*pluginv1.RemoteItem{dupA, dupB, anon, orphan}

	stats := e.reconcile(t)
	// dup (second), anonymous, and the dangling parent hint are skipped;
	// the first dup and the orphan item itself still sync.
	if stats.Created != 2 || stats.Skipped != 3 {
		t.Fatalf("stats = %+v, want 2 created, 3 skipped", stats)
	}
}
