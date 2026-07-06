package store

import (
	"context"
	"errors"
	"slices"
	"testing"

	taskpb "github.com/serteal/taskd/gen/task"
)

func TestSubtaskRoundTrip(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	parent := mustCreate(t, s, "parent")
	child, err := s.Create(ctx, "child", "", nil, nil, "", parent.GetId())
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}
	if child.GetParentId() != parent.GetId() {
		t.Errorf("child parent_id = %q, want %q", child.GetParentId(), parent.GetId())
	}
	// parent_id survives a read.
	got, err := s.Get(ctx, child.GetId())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.GetParentId() != parent.GetId() {
		t.Errorf("stored parent_id = %q, want %q", got.GetParentId(), parent.GetId())
	}
}

func TestSubtaskValidation(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	parent := mustCreate(t, s, "parent")
	child, err := s.Create(ctx, "child", "", nil, nil, "", parent.GetId())
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}

	// Missing parent -> ErrNotFound.
	if _, err := s.Create(ctx, "orphan", "", nil, nil, "", "no-such-id"); !errors.Is(err, ErrNotFound) {
		t.Errorf("create under missing parent err = %v, want ErrNotFound", err)
	}

	// Self-parent -> ErrInvalid (via update; a create can't reference its own id).
	if _, _, err := s.Update(ctx, parent.GetId(), 0, func(tk *taskpb.Task) error {
		tk.ParentId = parent.GetId()
		return nil
	}); !errors.Is(err, ErrInvalid) {
		t.Errorf("self-parent err = %v, want ErrInvalid", err)
	}

	// Depth downward: parenting a new task under an existing child (child is
	// itself a subtask) -> ErrInvalid.
	if _, err := s.Create(ctx, "grandchild", "", nil, nil, "", child.GetId()); !errors.Is(err, ErrInvalid) {
		t.Errorf("parent-is-a-subtask err = %v, want ErrInvalid", err)
	}

	// Depth upward: giving a task that has children a parent of its own ->
	// ErrInvalid (parent already has `child`).
	other := mustCreate(t, s, "other")
	if _, _, err := s.Update(ctx, parent.GetId(), 0, func(tk *taskpb.Task) error {
		tk.ParentId = other.GetId()
		return nil
	}); !errors.Is(err, ErrInvalid) {
		t.Errorf("task-with-children-gets-parent err = %v, want ErrInvalid", err)
	}
}

func TestSubtaskUnderSyncedParent(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	// A local checklist under a synced task (e.g. a PR) is supported.
	res := mustUpsert(t, s, "github", []*taskpb.ExternalTask{{ExternalRef: "org/repo#1", Title: "Fix bug"}}, nil, false)
	syncedParent := res.Changed[0]

	child, err := s.Create(ctx, "write test", "", nil, nil, "", syncedParent.GetId())
	if err != nil {
		t.Fatalf("Create under synced parent: %v", err)
	}
	if child.GetParentId() != syncedParent.GetId() {
		t.Errorf("child parent_id = %q, want the synced parent %q", child.GetParentId(), syncedParent.GetId())
	}
}

func TestDeleteReparentsChildren(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	parent := mustCreate(t, s, "parent")
	c1, _ := s.Create(ctx, "c1", "", nil, nil, "", parent.GetId())
	c2, _ := s.Create(ctx, "c2", "", nil, nil, "", parent.GetId())

	reparented, err := s.Delete(ctx, parent.GetId())
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(reparented) != 2 {
		t.Fatalf("re-parented %d children, want 2", len(reparented))
	}
	gotIDs := []string{reparented[0].GetId(), reparented[1].GetId()}
	slices.Sort(gotIDs)
	wantIDs := []string{c1.GetId(), c2.GetId()}
	slices.Sort(wantIDs)
	if !slices.Equal(gotIDs, wantIDs) {
		t.Errorf("re-parented ids = %v, want %v", gotIDs, wantIDs)
	}
	for _, r := range reparented {
		if r.GetParentId() != "" {
			t.Errorf("child %s still parented to %q", shortID(r.GetId()), r.GetParentId())
		}
		if r.GetRevision() != 2 {
			t.Errorf("child %s revision = %d, want 2 (bumped)", shortID(r.GetId()), r.GetRevision())
		}
	}
	// Children survive; the parent is gone.
	if _, err := s.Get(ctx, c1.GetId()); err != nil {
		t.Errorf("child deleted with parent: %v", err)
	}
	if _, err := s.Get(ctx, parent.GetId()); !errors.Is(err, ErrNotFound) {
		t.Errorf("parent still present: %v", err)
	}
}

func TestPruneReparentsOrphans(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	// A synced parent with a local child under it.
	res := mustUpsert(t, s, "github", []*taskpb.ExternalTask{{ExternalRef: "pr/1", Title: "PR"}}, nil, false)
	syncedParent := res.Changed[0]
	child, err := s.Create(ctx, "local checklist item", "", nil, nil, "", syncedParent.GetId())
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}

	// A full snapshot that omits the parent prunes it — but the child re-parents
	// to root rather than cascade-deleting.
	res = mustUpsert(t, s, "github", nil, nil, true)
	if res.Deleted != 1 {
		t.Fatalf("deleted = %d, want 1", res.Deleted)
	}
	if len(res.Reparented) != 1 || res.Reparented[0].GetId() != child.GetId() {
		t.Fatalf("Reparented = %v, want [%s]", res.Reparented, shortID(child.GetId()))
	}
	if res.Reparented[0].GetParentId() != "" {
		t.Errorf("orphan parent_id = %q, want cleared", res.Reparented[0].GetParentId())
	}
	got, err := s.Get(ctx, child.GetId())
	if err != nil {
		t.Fatalf("child lost after prune: %v", err)
	}
	if got.GetParentId() != "" {
		t.Errorf("stored child parent_id = %q, want cleared", got.GetParentId())
	}
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
