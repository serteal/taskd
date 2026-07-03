package ics

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"

	icspluginv1 "todoapp/gen/icsplugin/v1"
	pluginv1 "todoapp/gen/taskcore/plugin/v1"
	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/pkg/taskplugin"
)

// Manifest is the ics plugin's static self-description. Both kinds carry the
// icsplugin.v1.Event descriptors; calendar.event binds the schedulable due
// to the event's start — which is what makes `due < now + duration("24h")`
// work on mirrored calendar items with zero core changes.
func Manifest() *pluginv1.Manifest {
	types := taskplugin.DescriptorSet(&icspluginv1.Event{})
	return &pluginv1.Manifest{
		Name:     "ics",
		Version:  "0.1.0",
		Services: []string{"connector"},
		Kinds: []*pluginv1.KindRegistration{
			{
				Kind: "calendar.event",
				Facets: []*taskcorev1.FacetBinding{{
					Facet:    "schedulable",
					Bindings: map[string]string{"due": `item.mirror.data["event"].start`},
				}},
				Types: types,
			},
			{
				Kind:  "calendar.series",
				Types: types,
			},
		},
	}
}

// Connector implements taskplugin.Connector for one configured ICS source.
// Read-only, snapshot-enumerated: all the intelligence lives in Translate
// and the core's sync engine.
type Connector struct {
	// Now and Client are injectable for tests; zero values mean production.
	Now    func() time.Time
	Client *http.Client
	Log    *slog.Logger

	mu  sync.Mutex
	cfg Config
}

func (c *Connector) Configure(ctx context.Context, instance string, config *structpb.Struct) (*pluginv1.Capabilities, error) {
	cfg, err := ParseConfig(config)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.cfg = cfg
	c.mu.Unlock()
	return &pluginv1.Capabilities{
		Enumeration:  pluginv1.Enumeration_ENUMERATION_SNAPSHOT,
		PollInterval: durationpb.New(5 * time.Minute),
		// No intents: read-only. The core routes no writes here.
	}, nil
}

func (c *Connector) Snapshot(ctx context.Context, emit func(*pluginv1.RemoteItem) error) error {
	c.mu.Lock()
	cfg := c.cfg
	c.mu.Unlock()

	now := time.Now().UTC()
	if c.Now != nil {
		now = c.Now()
	}
	payload, err := Fetch(ctx, c.Client, cfg)
	if err != nil {
		return fmt.Errorf("fetching %s: %w", cfg.URL, err)
	}
	items, warnings, err := Translate(payload, now, cfg.HorizonDays)
	if err != nil {
		return err
	}
	for _, w := range warnings {
		if c.Log != nil {
			c.Log.Warn("ics translation warning", "warning", w)
		}
	}
	for _, it := range items {
		if err := emit(it); err != nil {
			return err
		}
	}
	return nil
}
