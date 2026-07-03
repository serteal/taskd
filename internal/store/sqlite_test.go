package store_test

import (
	"context"
	"encoding/base64"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/clock"
	"todoapp/internal/feed"
	"todoapp/internal/store"
)

var testProv = &taskcorev1.Provenance{
	Source: taskcorev1.ProvenanceSource_PROVENANCE_SOURCE_CLIENT,
	Ref:    "test",
}

// extract derives the indexed columns straight from the todo layer — enough
// for store tests (the real query.Extract adds facet bindings).
func extract(item *taskcorev1.Item) store.Indexed {
	idx := store.Indexed{
		Completed: item.GetTodo().GetCompleted(),
		Project:   item.GetTodo().GetProject(),
	}
	if d := item.GetTodo().GetDue(); d != nil {
		t := d.AsTime()
		idx.Due = &t
	}
	if sn := item.GetTodo().GetSnoozedUntil(); sn != nil {
		t := sn.AsTime()
		idx.SnoozedUntil = &t
	}
	return idx
}

type env struct {
	st   store.Store
	fake *clock.Fake
	ids  clock.IDGen
	opts store.Options
}

func newEnv(t *testing.T) *env {
	t.Helper()
	fake := clock.NewFake(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	opts := store.Options{
		Path:    filepath.Join(t.TempDir(), "todo.db"),
		Diff:    feed.Diff,
		Extract: extract,
		Now:     fake.Now,
	}
	st, err := store.Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return &env{st: st, fake: fake, ids: clock.NewIDGen(fake, 1), opts: opts}
}

// newItem builds a minimal native item; mut customizes it before use.
func (e *env) newItem(mut func(*taskcorev1.Item)) *taskcorev1.Item {
	item := &taskcorev1.Item{
		Id:        e.ids.NewID(),
		Kind:      "task",
		Todo:      &taskcorev1.Todo{},
		CreatedAt: timestamppb.New(e.fake.Now()),
	}
	if mut != nil {
		mut(item)
	}
	return item
}

func (e *env) mustCreate(t *testing.T, item *taskcorev1.Item) *taskcorev1.Event {
	t.Helper()
	evt, err := e.st.CreateItem(context.Background(), item, testProv)
	if err != nil {
		t.Fatalf("CreateItem(%s): %v", item.GetId(), err)
	}
	return evt
}

func TestCreateGetRoundTrip(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	item := e.newItem(func(it *taskcorev1.Item) {
		it.Kind = "linear.issue"
		it.Todo = &taskcorev1.Todo{
			Labels:  []string{"urgent", "review"},
			Project: "work/reviews",
			Due:     timestamppb.New(e.fake.Now().Add(48 * time.Hour)),
			Note:    "round trip",
		}
		it.Mirror = &taskcorev1.Mirror{
			Link: &taskcorev1.ExternalLink{
				ConnectorInstance: "linear@work",
				ExternalId:        "ENG-42",
				Etag:              "e1",
			},
			Title: "Fix flaky test",
			State: "open",
			Data: map[string]*anypb.Any{
				"issue": {TypeUrl: "type.googleapis.com/x.Issue", Value: []byte{1, 2, 3}},
			},
		}
		it.Relations = []*taskcorev1.Relation{
			{Type: taskcorev1.RelationType_RELATION_TYPE_BLOCKS, TargetId: "other"},
		}
	})
	original := proto.Clone(item).(*taskcorev1.Item)

	evt := e.mustCreate(t, item)

	want := proto.Clone(original).(*taskcorev1.Item)
	want.UpdatedAt = timestamppb.New(e.fake.Now()) // set by the store

	got, err := e.st.GetItem(ctx, item.GetId())
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if !proto.Equal(got, want) {
		t.Errorf("round trip mismatch:\n got: %v\nwant: %v", got, want)
	}

	if evt.GetType() != taskcorev1.ChangeType_CHANGE_TYPE_CREATED {
		t.Errorf("event type = %v, want CREATED", evt.GetType())
	}
	if evt.GetCursor() != 1 {
		t.Errorf("event cursor = %d, want 1", evt.GetCursor())
	}
	if len(evt.GetChanges()) != 0 {
		t.Errorf("CREATED event has %d changes, want 0", len(evt.GetChanges()))
	}
	if evt.GetItemId() != item.GetId() {
		t.Errorf("event item_id = %q, want %q", evt.GetItemId(), item.GetId())
	}
	if !proto.Equal(evt.GetItem(), want) {
		t.Errorf("event after-image mismatch:\n got: %v\nwant: %v", evt.GetItem(), want)
	}
	if !proto.Equal(evt.GetCausedBy(), testProv) {
		t.Errorf("event caused_by = %v, want %v", evt.GetCausedBy(), testProv)
	}
	// The caller's item is not mutated by the store.
	if !proto.Equal(item, original) {
		t.Errorf("CreateItem mutated the caller's item")
	}

	if _, err := e.st.GetItem(ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetItem(missing) = %v, want ErrNotFound", err)
	}
}

func TestMutateTodoBumpsOnlyTodoRevision(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	item := e.newItem(nil)
	e.mustCreate(t, item)

	mutatedAt := e.fake.Advance(time.Minute)
	got, evt, err := e.st.MutateItem(ctx, item.GetId(), testProv, func(it *taskcorev1.Item) error {
		it.Todo.Note = "hello"
		return nil
	})
	if err != nil {
		t.Fatalf("MutateItem: %v", err)
	}
	if got.GetTodoRevision() != 1 {
		t.Errorf("todo_revision = %d, want 1", got.GetTodoRevision())
	}
	if got.GetMirrorRevision() != 0 {
		t.Errorf("mirror_revision = %d, want 0", got.GetMirrorRevision())
	}
	if !got.GetUpdatedAt().AsTime().Equal(mutatedAt) {
		t.Errorf("updated_at = %v, want %v", got.GetUpdatedAt().AsTime(), mutatedAt)
	}
	if evt.GetType() != taskcorev1.ChangeType_CHANGE_TYPE_UPDATED {
		t.Errorf("event type = %v, want UPDATED", evt.GetType())
	}
	if evt.GetCursor() != 2 {
		t.Errorf("event cursor = %d, want 2", evt.GetCursor())
	}
	if len(evt.GetChanges()) != 1 || evt.GetChanges()[0].GetPath() != "todo.note" {
		t.Errorf("changes = %v, want single todo.note", evt.GetChanges())
	}
	stored, err := e.st.GetItem(ctx, item.GetId())
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if !proto.Equal(stored, got) {
		t.Errorf("stored item != returned item")
	}
	if !proto.Equal(evt.GetItem(), got) {
		t.Errorf("event after-image != returned item")
	}
}

func TestMutateMirrorBumpsOnlyMirrorRevision(t *testing.T) {
	e := newEnv(t)
	item := e.newItem(func(it *taskcorev1.Item) {
		it.Kind = "github.pr"
		it.Mirror = &taskcorev1.Mirror{State: "open"}
	})
	e.mustCreate(t, item)

	got, _, err := e.st.MutateItem(context.Background(), item.GetId(), testProv, func(it *taskcorev1.Item) error {
		it.Mirror.State = "merged"
		return nil
	})
	if err != nil {
		t.Fatalf("MutateItem: %v", err)
	}
	if got.GetMirrorRevision() != 1 {
		t.Errorf("mirror_revision = %d, want 1", got.GetMirrorRevision())
	}
	if got.GetTodoRevision() != 0 {
		t.Errorf("todo_revision = %d, want 0", got.GetTodoRevision())
	}
}

func TestMutateRelationsBumpsTodoRevision(t *testing.T) {
	// Relations count as todo-layer in phase 1 (the user is their only writer).
	e := newEnv(t)
	item := e.newItem(nil)
	e.mustCreate(t, item)

	got, evt, err := e.st.MutateItem(context.Background(), item.GetId(), testProv, func(it *taskcorev1.Item) error {
		it.Relations = append(it.Relations, &taskcorev1.Relation{
			Type: taskcorev1.RelationType_RELATION_TYPE_BLOCKS, TargetId: "x",
		})
		return nil
	})
	if err != nil {
		t.Fatalf("MutateItem: %v", err)
	}
	if got.GetTodoRevision() != 1 {
		t.Errorf("todo_revision = %d, want 1", got.GetTodoRevision())
	}
	if got.GetMirrorRevision() != 0 {
		t.Errorf("mirror_revision = %d, want 0", got.GetMirrorRevision())
	}
	if len(evt.GetChanges()) != 1 || evt.GetChanges()[0].GetPath() != "relations" {
		t.Errorf("changes = %v, want single relations", evt.GetChanges())
	}
}

func TestMutateNoopWritesNothing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	item := e.newItem(func(it *taskcorev1.Item) { it.Todo.Note = "same" })
	e.mustCreate(t, item)
	createdAt := e.fake.Now()

	e.fake.Advance(time.Hour)
	got, evt, err := e.st.MutateItem(ctx, item.GetId(), testProv, func(it *taskcorev1.Item) error {
		it.Todo.Note = "same" // echo: same value back
		return nil
	})
	if err != nil {
		t.Fatalf("MutateItem: %v", err)
	}
	if evt != nil {
		t.Errorf("no-op mutate produced event %v, want nil", evt)
	}
	if got.GetTodoRevision() != 0 || got.GetMirrorRevision() != 0 {
		t.Errorf("no-op bumped revisions: todo=%d mirror=%d", got.GetTodoRevision(), got.GetMirrorRevision())
	}
	if !got.GetUpdatedAt().AsTime().Equal(createdAt) {
		t.Errorf("no-op changed updated_at to %v", got.GetUpdatedAt().AsTime())
	}
	latest, err := e.st.LatestCursor(ctx)
	if err != nil {
		t.Fatalf("LatestCursor: %v", err)
	}
	if latest != 1 {
		t.Errorf("LatestCursor = %d after no-op, want 1", latest)
	}
	stored, err := e.st.GetItem(ctx, item.GetId())
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if !stored.GetUpdatedAt().AsTime().Equal(createdAt) {
		t.Errorf("no-op persisted a new updated_at %v", stored.GetUpdatedAt().AsTime())
	}
}

func TestMutateFnErrorAborts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	item := e.newItem(nil)
	e.mustCreate(t, item)

	boom := errors.New("boom")
	_, _, err := e.st.MutateItem(ctx, item.GetId(), testProv, func(it *taskcorev1.Item) error {
		it.Todo.Note = "will be discarded"
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("MutateItem err = %v, want boom", err)
	}
	stored, err := e.st.GetItem(ctx, item.GetId())
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if stored.GetTodo().GetNote() != "" {
		t.Errorf("aborted mutation persisted: note = %q", stored.GetTodo().GetNote())
	}
	if latest, _ := e.st.LatestCursor(ctx); latest != 1 {
		t.Errorf("LatestCursor = %d after aborted mutate, want 1", latest)
	}

	if _, _, err := e.st.MutateItem(ctx, "missing", testProv, func(*taskcorev1.Item) error { return nil }); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("MutateItem(missing) = %v, want ErrNotFound", err)
	}
}

func TestDeleteEmitsLastState(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	item := e.newItem(nil)
	e.mustCreate(t, item)
	last, _, err := e.st.MutateItem(ctx, item.GetId(), testProv, func(it *taskcorev1.Item) error {
		it.Todo.Note = "final form"
		return nil
	})
	if err != nil {
		t.Fatalf("MutateItem: %v", err)
	}

	evt, err := e.st.DeleteItem(ctx, item.GetId(), testProv)
	if err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}
	if evt.GetType() != taskcorev1.ChangeType_CHANGE_TYPE_DELETED {
		t.Errorf("event type = %v, want DELETED", evt.GetType())
	}
	if evt.GetCursor() != 3 {
		t.Errorf("event cursor = %d, want 3", evt.GetCursor())
	}
	if len(evt.GetChanges()) != 0 {
		t.Errorf("DELETED event has %d changes, want 0", len(evt.GetChanges()))
	}
	if !proto.Equal(evt.GetItem(), last) {
		t.Errorf("DELETED after-image != last state:\n got: %v\nwant: %v", evt.GetItem(), last)
	}
	if _, err := e.st.GetItem(ctx, item.GetId()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetItem after delete = %v, want ErrNotFound", err)
	}
	if _, err := e.st.DeleteItem(ctx, "missing", testProv); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("DeleteItem(missing) = %v, want ErrNotFound", err)
	}
}

func TestEventOrderingAndCursors(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	var ids []string
	for range 3 {
		item := e.newItem(nil)
		e.mustCreate(t, item)
		ids = append(ids, item.GetId())
	}

	latest, err := e.st.LatestCursor(ctx)
	if err != nil || latest != 3 {
		t.Errorf("LatestCursor = %d, %v; want 3", latest, err)
	}
	oldest, err := e.st.OldestCursor(ctx)
	if err != nil || oldest != 1 {
		t.Errorf("OldestCursor = %d, %v; want 1", oldest, err)
	}

	events, err := e.st.ListEvents(ctx, 0, 10)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("ListEvents returned %d events, want 3", len(events))
	}
	for i, evt := range events {
		if evt.GetCursor() != uint64(i+1) {
			t.Errorf("event[%d].cursor = %d, want %d", i, evt.GetCursor(), i+1)
		}
		if evt.GetItemId() != ids[i] {
			t.Errorf("event[%d].item_id = %q, want %q", i, evt.GetItemId(), ids[i])
		}
		if evt.GetType() != taskcorev1.ChangeType_CHANGE_TYPE_CREATED {
			t.Errorf("event[%d].type = %v, want CREATED", i, evt.GetType())
		}
	}

	since, err := e.st.ListEvents(ctx, 1, 10)
	if err != nil {
		t.Fatalf("ListEvents(since 1): %v", err)
	}
	if len(since) != 2 || since[0].GetCursor() != 2 || since[1].GetCursor() != 3 {
		t.Errorf("ListEvents(since 1) cursors wrong: %v", since)
	}

	limited, err := e.st.ListEvents(ctx, 0, 2)
	if err != nil {
		t.Fatalf("ListEvents(limit 2): %v", err)
	}
	if len(limited) != 2 || limited[0].GetCursor() != 1 || limited[1].GetCursor() != 2 {
		t.Errorf("ListEvents(limit 2) wrong: %v", limited)
	}

	// Empty-log cursors are 0.
	e2 := newEnv(t)
	if latest, _ := e2.st.LatestCursor(ctx); latest != 0 {
		t.Errorf("empty LatestCursor = %d, want 0", latest)
	}
	if oldest, _ := e2.st.OldestCursor(ctx); oldest != 0 {
		t.Errorf("empty OldestCursor = %d, want 0", oldest)
	}
}

func TestTrimEvents(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	e.mustCreate(t, e.newItem(nil)) // cursor 1 at t0
	e.fake.Advance(time.Hour)
	cutoff := e.fake.Now()
	e.mustCreate(t, e.newItem(nil)) // cursor 2 at t0+1h
	e.fake.Advance(time.Hour)
	e.mustCreate(t, e.newItem(nil)) // cursor 3 at t0+2h

	removed, err := e.st.TrimEvents(ctx, cutoff)
	if err != nil {
		t.Fatalf("TrimEvents: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	if oldest, _ := e.st.OldestCursor(ctx); oldest != 2 {
		t.Errorf("OldestCursor = %d after trim, want 2", oldest)
	}

	// Full trim: everything is older than the cutoff, but the newest event
	// must survive so LatestCursor does.
	removed, err = e.st.TrimEvents(ctx, e.fake.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("TrimEvents(full): %v", err)
	}
	if removed != 1 {
		t.Errorf("full trim removed = %d, want 1", removed)
	}
	latest, _ := e.st.LatestCursor(ctx)
	oldest, _ := e.st.OldestCursor(ctx)
	if latest != 3 || oldest != 3 {
		t.Errorf("after full trim latest=%d oldest=%d, want 3/3", latest, oldest)
	}
	events, err := e.st.ListEvents(ctx, 0, 10)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].GetCursor() != 3 {
		t.Errorf("after full trim events = %v, want just cursor 3", events)
	}
}

func TestResolveIDPrefix(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, id := range []string{"AAA", "AAA111", "AAB222"} {
		item := e.newItem(func(it *taskcorev1.Item) { it.Id = id })
		e.mustCreate(t, item)
	}

	// Exact match wins even though it prefixes AAA111.
	if got, err := e.st.ResolveIDPrefix(ctx, "AAA"); err != nil || got != "AAA" {
		t.Errorf("ResolveIDPrefix(AAA) = %q, %v; want AAA", got, err)
	}
	// Unique prefix expands.
	if got, err := e.st.ResolveIDPrefix(ctx, "AAB"); err != nil || got != "AAB222" {
		t.Errorf("ResolveIDPrefix(AAB) = %q, %v; want AAB222", got, err)
	}
	// Ambiguous prefix.
	if _, err := e.st.ResolveIDPrefix(ctx, "AA"); !errors.Is(err, store.ErrAmbiguousPrefix) {
		t.Errorf("ResolveIDPrefix(AA) = %v, want ErrAmbiguousPrefix", err)
	}
	// No match.
	if _, err := e.st.ResolveIDPrefix(ctx, "ZZZ"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("ResolveIDPrefix(ZZZ) = %v, want ErrNotFound", err)
	}
}

func TestViewsCRUD(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	today := &taskcorev1.View{Name: "today", Filter: "effective_due <= today", OrderBy: "due"}
	if err := e.st.SaveView(ctx, today); err != nil {
		t.Fatalf("SaveView: %v", err)
	}
	got, err := e.st.GetView(ctx, "today")
	if err != nil || !proto.Equal(got, today) {
		t.Errorf("GetView = %v, %v; want %v", got, err, today)
	}

	// Overwrite by name.
	today2 := &taskcorev1.View{Name: "today", Filter: "!completed", OrderBy: "due", GroupBy: "project"}
	if err := e.st.SaveView(ctx, today2); err != nil {
		t.Fatalf("SaveView(overwrite): %v", err)
	}
	got, err = e.st.GetView(ctx, "today")
	if err != nil || !proto.Equal(got, today2) {
		t.Errorf("GetView after overwrite = %v, %v; want %v", got, err, today2)
	}

	inbox := &taskcorev1.View{Name: "inbox", Filter: "!has(todo)"}
	if err := e.st.SaveView(ctx, inbox); err != nil {
		t.Fatalf("SaveView(inbox): %v", err)
	}
	views, err := e.st.ListViews(ctx)
	if err != nil {
		t.Fatalf("ListViews: %v", err)
	}
	if len(views) != 2 || !proto.Equal(views[0], inbox) || !proto.Equal(views[1], today2) {
		t.Errorf("ListViews = %v, want [inbox today2] by name", views)
	}

	if err := e.st.DeleteView(ctx, "today"); err != nil {
		t.Fatalf("DeleteView: %v", err)
	}
	if _, err := e.st.GetView(ctx, "today"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetView(deleted) = %v, want ErrNotFound", err)
	}
	if views, _ := e.st.ListViews(ctx); len(views) != 1 {
		t.Errorf("ListViews after delete has %d views, want 1", len(views))
	}
	if _, err := e.st.GetView(ctx, "never"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetView(never) = %v, want ErrNotFound", err)
	}
}

func TestQueryWherePushdownWithResidual(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	mk := func(project, note string, completed bool) string {
		item := e.newItem(func(it *taskcorev1.Item) {
			it.Todo.Project = project
			it.Todo.Note = note
			it.Todo.Completed = completed
		})
		e.mustCreate(t, item)
		return item.GetId()
	}
	wantID := mk("work", "keep", false)
	mk("work", "drop", false)   // fails residual
	mk("work", "keep", true)    // fails Where (completed)
	mk("home", "keep", false)   // fails Where (project)
	mk("work/x", "keep", false) // fails Where (project)

	res, err := e.st.QueryItems(ctx, store.Query{
		Where: "project = ? AND completed = ?",
		Args:  []any{"work", 0},
		Residual: func(it *taskcorev1.Item) (bool, error) {
			return it.GetTodo().GetNote() == "keep", nil
		},
	})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if len(res.Items) != 1 || res.Items[0].GetId() != wantID {
		t.Errorf("QueryItems returned %d items, want just %s", len(res.Items), wantID)
	}
	if res.NextPageToken != "" {
		t.Errorf("NextPageToken = %q, want empty", res.NextPageToken)
	}
	latest, _ := e.st.LatestCursor(ctx)
	if res.Cursor != latest {
		t.Errorf("QueryResult.Cursor = %d, want %d", res.Cursor, latest)
	}
}

func TestQueryPaginationWithResidual(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	var ids []string
	for i := range 12 {
		note := "keep"
		if i%3 == 0 {
			note = "skip" // drops 0, 3, 6, 9 — 8 items survive
		}
		item := e.newItem(func(it *taskcorev1.Item) { it.Todo.Note = note })
		e.mustCreate(t, item)
		ids = append(ids, item.GetId())
	}

	// Default order is created_at desc; created_at ties, so the id desc
	// tiebreaker gives exactly reverse creation order.
	var want []string
	for i := len(ids) - 1; i >= 0; i-- {
		if i%3 != 0 {
			want = append(want, ids[i])
		}
	}

	var got []string
	token := ""
	pages := 0
	for {
		res, err := e.st.QueryItems(ctx, store.Query{
			Residual: func(it *taskcorev1.Item) (bool, error) {
				return it.GetTodo().GetNote() == "keep", nil
			},
			PageSize:  3,
			PageToken: token,
		})
		if err != nil {
			t.Fatalf("QueryItems page %d: %v", pages, err)
		}
		pages++
		for _, it := range res.Items {
			got = append(got, it.GetId())
		}
		if res.NextPageToken == "" {
			break
		}
		token = res.NextPageToken
		if pages > 10 {
			t.Fatal("runaway pagination")
		}
	}
	if pages < 3 {
		t.Errorf("paginated in %d pages, want >= 3", pages)
	}
	if !slices.Equal(got, want) {
		t.Errorf("pagination lost or duplicated items:\n got: %v\nwant: %v", got, want)
	}
}

func TestQueryOrderByDueNullsLast(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	base := e.fake.Now()

	mk := func(due *timestamppb.Timestamp) string {
		item := e.newItem(func(it *taskcorev1.Item) { it.Todo.Due = due })
		e.mustCreate(t, item)
		return item.GetId()
	}
	a := mk(timestamppb.New(base.Add(1 * time.Hour)))
	b := mk(timestamppb.New(base.Add(2 * time.Hour)))
	c := mk(timestamppb.New(base.Add(3 * time.Hour)))
	d := mk(nil)
	f := mk(nil)

	queryIDs := func(desc bool) []string {
		res, err := e.st.QueryItems(ctx, store.Query{OrderBy: "due", Desc: desc})
		if err != nil {
			t.Fatalf("QueryItems(due, desc=%v): %v", desc, err)
		}
		var out []string
		for _, it := range res.Items {
			out = append(out, it.GetId())
		}
		return out
	}

	// asc: dued items by due, then null dues last (id asc among them).
	if got, want := queryIDs(false), []string{a, b, c, d, f}; !slices.Equal(got, want) {
		t.Errorf("due asc:\n got: %v\nwant: %v", got, want)
	}
	// desc: dues descend, nulls still last (id desc among them).
	if got, want := queryIDs(true), []string{c, b, a, f, d}; !slices.Equal(got, want) {
		t.Errorf("due desc:\n got: %v\nwant: %v", got, want)
	}
}

func TestQueryInvalidPageTokenAndOrderBy(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	if _, err := e.st.QueryItems(ctx, store.Query{PageToken: "!!!not-base64!!!"}); !errors.Is(err, store.ErrInvalidPageToken) {
		t.Errorf("garbage token err = %v, want ErrInvalidPageToken", err)
	}
	nonNumeric := base64.StdEncoding.EncodeToString([]byte("abc"))
	if _, err := e.st.QueryItems(ctx, store.Query{PageToken: nonNumeric}); !errors.Is(err, store.ErrInvalidPageToken) {
		t.Errorf("non-numeric token err = %v, want ErrInvalidPageToken", err)
	}
	if _, err := e.st.QueryItems(ctx, store.Query{OrderBy: "blob; DROP TABLE items"}); err == nil {
		t.Error("unvalidated order_by was accepted")
	}
	// Empty store queries cleanly.
	res, err := e.st.QueryItems(ctx, store.Query{})
	if err != nil {
		t.Fatalf("QueryItems(empty): %v", err)
	}
	if len(res.Items) != 0 || res.NextPageToken != "" || res.Cursor != 0 {
		t.Errorf("empty store result = %+v", res)
	}
}

func TestBackup(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	first := e.newItem(func(it *taskcorev1.Item) { it.Todo.Note = "backed up" })
	e.mustCreate(t, first)
	second := e.newItem(nil)
	e.mustCreate(t, second)

	dest := filepath.Join(t.TempDir(), "backup.db")
	size, err := e.st.Backup(ctx, dest)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if size <= 0 {
		t.Errorf("backup size = %d, want > 0", size)
	}

	// The backup opens as a valid database containing the items and events.
	opts := e.opts
	opts.Path = dest
	restored, err := store.Open(opts)
	if err != nil {
		t.Fatalf("Open(backup): %v", err)
	}
	defer restored.Close()
	got, err := restored.GetItem(ctx, first.GetId())
	if err != nil {
		t.Fatalf("GetItem from backup: %v", err)
	}
	if got.GetTodo().GetNote() != "backed up" {
		t.Errorf("backup item note = %q, want %q", got.GetTodo().GetNote(), "backed up")
	}
	if latest, _ := restored.LatestCursor(ctx); latest != 2 {
		t.Errorf("backup LatestCursor = %d, want 2", latest)
	}

	// Backing up onto an existing file fails with the wrapped sqlite error.
	if _, err := e.st.Backup(ctx, dest); err == nil {
		t.Error("Backup over existing file succeeded, want error")
	}
}
