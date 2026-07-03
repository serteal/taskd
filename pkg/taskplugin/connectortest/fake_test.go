package connectortest_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
	"todoapp/pkg/taskplugin"
	"todoapp/pkg/taskplugin/connectortest"
)

// fakeDriver is the trivial Driver over the in-memory Fake.
type fakeDriver struct{ f *connectortest.Fake }

func (d fakeDriver) Connector() taskplugin.Connector           { return d.f }
func (d fakeDriver) Put(_ *testing.T, it *pluginv1.RemoteItem) { d.f.Put(it) }
func (d fakeDriver) Delete(_ *testing.T, externalID string)    { d.f.Delete(externalID) }

// TestConf runs the conformance suite against the reference Fake.
func TestConf(t *testing.T) {
	connectortest.RunConformance(t, func(t *testing.T) connectortest.Driver {
		f := connectortest.NewFake("fake@conf")
		if _, err := f.Configure(context.Background(), "fake@conf", nil); err != nil {
			t.Fatalf("Configure: %v", err)
		}
		return fakeDriver{f}
	})
}

func TestFailNext(t *testing.T) {
	ctx := context.Background()
	f := connectortest.NewFake("fake@x")
	f.Put(&pluginv1.RemoteItem{ExternalId: "a", Kind: "k", Title: "A"})

	boom := errors.New("boom")
	f.FailNextSnapshot(boom)

	if _, err := f.SnapshotList(ctx); !errors.Is(err, boom) {
		t.Fatalf("first snapshot error = %v, want %v", err, boom)
	}
	items, err := f.SnapshotList(ctx)
	if err != nil {
		t.Fatalf("second snapshot must recover, got error: %v", err)
	}
	if len(items) != 1 || items[0].GetExternalId() != "a" {
		t.Fatalf("second snapshot = %v, want the one item back", items)
	}
}

func TestCaps(t *testing.T) {
	f := connectortest.NewFake("fake@caps")
	caps, err := f.Configure(context.Background(), "fake@caps", nil)
	if err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if caps.GetEnumeration() != pluginv1.Enumeration_ENUMERATION_SNAPSHOT {
		t.Errorf("enumeration = %v, want SNAPSHOT", caps.GetEnumeration())
	}
	if caps.GetPollInterval().AsDuration().Seconds() != 60 {
		t.Errorf("poll interval = %v, want 1m", caps.GetPollInterval().AsDuration())
	}
}

// TestRace hammers the fake concurrently; the race detector is the assert.
func TestRace(t *testing.T) {
	f := connectortest.NewFake("fake@race")
	ctx := context.Background()
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				id := fmt.Sprintf("g%d-%d", g, i%5)
				f.Put(&pluginv1.RemoteItem{ExternalId: id, Kind: "k", Title: id})
				if i%7 == 0 {
					f.Delete(id)
				}
				if i%11 == 0 {
					f.FailNextSnapshot(errors.New("transient"))
				}
				_, _ = f.SnapshotList(ctx)
			}
		}(g)
	}
	wg.Wait()
}

// TestClone verifies snapshots are isolated from later caller mutation.
func TestClone(t *testing.T) {
	ctx := context.Background()
	f := connectortest.NewFake("fake@clone")
	in := &pluginv1.RemoteItem{ExternalId: "a", Kind: "k", Title: "A"}
	f.Put(in)
	in.Title = "mutated after Put"

	items, err := f.SnapshotList(ctx)
	if err != nil {
		t.Fatalf("SnapshotList: %v", err)
	}
	if items[0].GetTitle() != "A" {
		t.Errorf("Put did not clone: title = %q, want A", items[0].GetTitle())
	}
	items[0].Title = "mutated after snapshot"
	again, err := f.SnapshotList(ctx)
	if err != nil {
		t.Fatalf("SnapshotList: %v", err)
	}
	if again[0].GetTitle() != "A" {
		t.Errorf("Snapshot did not clone: title = %q, want A", again[0].GetTitle())
	}
}
