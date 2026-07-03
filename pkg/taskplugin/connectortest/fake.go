// Package connectortest is the connector conformance kit (DESIGN.md §16):
// Fake is a scripted in-memory remote — "object appears, changes,
// disappears, comes back" — that serves as the reference Connector and the
// core's test double, and RunConformance is the scenario suite every real
// connector runs in CI via a small Driver adapter.
package connectortest

import (
	"context"
	"sort"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
	"todoapp/pkg/taskplugin"
)

// Fake is an in-memory scripted connector: a remote you mutate directly with
// Put/Delete. It implements taskplugin.Connector; snapshots enumerate the
// current contents sorted by external_id, and repeated snapshots of an
// unchanged remote are byte-identical (the stability the core's no-op
// suppression depends on). Concurrency-safe.
type Fake struct {
	mu       sync.Mutex
	instance string
	items    map[string]*pluginv1.RemoteItem // keyed by external_id
	nextErr  error
}

var _ taskplugin.Connector = (*Fake)(nil)

// NewFake returns an empty fake remote for the given instance name.
func NewFake(instance string) *Fake {
	return &Fake{
		instance: instance,
		items:    make(map[string]*pluginv1.RemoteItem),
	}
}

// Configure implements taskplugin.Connector: it accepts any config, records
// the instance name, and reports SNAPSHOT enumeration with a 1m poll hint.
func (f *Fake) Configure(_ context.Context, instance string, _ *structpb.Struct) (*pluginv1.Capabilities, error) {
	f.mu.Lock()
	f.instance = instance
	f.mu.Unlock()
	return &pluginv1.Capabilities{
		Enumeration:  pluginv1.Enumeration_ENUMERATION_SNAPSHOT,
		PollInterval: durationpb.New(time.Minute),
	}, nil
}

// Put upserts items into the remote, keyed by external_id. Items are cloned
// on the way in, so callers may keep mutating their copies.
func (f *Fake) Put(items ...*pluginv1.RemoteItem) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, it := range items {
		f.items[it.GetExternalId()] = proto.Clone(it).(*pluginv1.RemoteItem)
	}
}

// Delete removes objects from the remote. Deleting an absent id is a no-op —
// remotes don't error on already-gone objects, they just don't enumerate
// them.
func (f *Fake) Delete(externalIDs ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range externalIDs {
		delete(f.items, id)
	}
}

// FailNextSnapshot makes exactly the next Snapshot (or SnapshotList) return
// err before emitting anything; the one after that succeeds again. Use it to
// exercise the core's retry path.
func (f *Fake) FailNextSnapshot(err error) {
	f.mu.Lock()
	f.nextErr = err
	f.mu.Unlock()
}

// Snapshot implements taskplugin.Connector: it emits a clone of every item,
// sorted by external_id.
func (f *Fake) Snapshot(ctx context.Context, emit func(*pluginv1.RemoteItem) error) error {
	f.mu.Lock()
	if err := f.nextErr; err != nil {
		f.nextErr = nil
		f.mu.Unlock()
		return err
	}
	out := make([]*pluginv1.RemoteItem, 0, len(f.items))
	for _, it := range f.items {
		out = append(out, proto.Clone(it).(*pluginv1.RemoteItem))
	}
	f.mu.Unlock()

	sort.Slice(out, func(i, j int) bool { return out[i].GetExternalId() < out[j].GetExternalId() })
	for _, it := range out {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := emit(it); err != nil {
			return err
		}
	}
	return nil
}

// SnapshotList collects one Snapshot into a slice. It is the adapter seam:
// callers that need the fake behind some other enumeration interface (the
// core's sync source, say) wrap this instead of connectortest importing
// their packages.
func (f *Fake) SnapshotList(ctx context.Context) ([]*pluginv1.RemoteItem, error) {
	items := []*pluginv1.RemoteItem{}
	err := f.Snapshot(ctx, func(it *pluginv1.RemoteItem) error {
		items = append(items, it)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return items, nil
}
