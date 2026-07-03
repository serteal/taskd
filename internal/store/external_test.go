package store_test

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/protobuf/proto"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/store"
)

func mirroredItem(e *env, instance, externalID, title string) *taskcorev1.Item {
	it := e.newItem(nil)
	it.Todo = nil
	it.Mirror = &taskcorev1.Mirror{
		Title: title,
		State: "confirmed",
		Link: &taskcorev1.ExternalLink{
			ConnectorInstance: instance,
			ExternalId:        externalID,
		},
	}
	return it
}

func TestExternalLookup(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	a := mirroredItem(e, "cal@p", "uid-1", "standup")
	b := mirroredItem(e, "cal@p", "uid-2", "retro")
	c := mirroredItem(e, "cal@work", "uid-1", "other calendar, same uid")
	for _, it := range []*taskcorev1.Item{a, b, c} {
		e.mustCreate(t, it)
	}

	got, err := e.st.GetItemByExternal(ctx, "cal@p", "uid-1")
	if err != nil {
		t.Fatalf("GetItemByExternal: %v", err)
	}
	if got.GetId() != a.GetId() {
		t.Fatalf("got %s, want %s (instances must namespace external ids)", got.GetId(), a.GetId())
	}

	if _, err := e.st.GetItemByExternal(ctx, "cal@p", "uid-404"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing external id: err = %v, want ErrNotFound", err)
	}

	items, err := e.st.ListInstanceItems(ctx, "cal@p")
	if err != nil {
		t.Fatalf("ListInstanceItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("instance items = %d, want 2", len(items))
	}

	// The external columns must track mutation too: re-link is visible.
	if _, _, err := e.st.MutateItem(ctx, a.GetId(), testProv, func(it *taskcorev1.Item) error {
		it.Mirror.Link.ExternalId = "uid-1-renamed"
		return nil
	}); err != nil {
		t.Fatalf("mutate link: %v", err)
	}
	if _, err := e.st.GetItemByExternal(ctx, "cal@p", "uid-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("old external id must be gone after relink, err = %v", err)
	}
	if _, err := e.st.GetItemByExternal(ctx, "cal@p", "uid-1-renamed"); err != nil {
		t.Fatalf("new external id must resolve: %v", err)
	}
}

func TestManifests(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	m := &pluginv1.Manifest{
		Name:     "ics",
		Version:  "0.1.0",
		Services: []string{"connector"},
		Kinds: []*pluginv1.KindRegistration{{
			Kind: "calendar.event",
			Facets: []*taskcorev1.FacetBinding{{
				Facet:    "schedulable",
				Bindings: map[string]string{"due": `item.mirror.data["event"].start`},
			}},
		}},
	}
	if err := e.st.SaveManifest(ctx, m); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}

	got, err := e.st.GetManifest(ctx, "ics")
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if !proto.Equal(m, got) {
		t.Fatalf("manifest round-trip mismatch:\n got %v\nwant %v", got, m)
	}

	// Upsert by plugin name.
	m2 := proto.Clone(m).(*pluginv1.Manifest)
	m2.Version = "0.2.0"
	if err := e.st.SaveManifest(ctx, m2); err != nil {
		t.Fatalf("SaveManifest upsert: %v", err)
	}
	all, err := e.st.ListManifests(ctx)
	if err != nil {
		t.Fatalf("ListManifests: %v", err)
	}
	if len(all) != 1 || all[0].GetVersion() != "0.2.0" {
		t.Fatalf("manifests = %v, want single ics@0.2.0", all)
	}

	if _, err := e.st.GetManifest(ctx, "ghost"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing manifest: err = %v, want ErrNotFound", err)
	}
}
