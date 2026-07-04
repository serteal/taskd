// Package syncer runs integrations. A syncer is deliberately just another
// API client: it reconciles an external system into tasks through
// TaskService.UpsertExternalTasks and has no privileged access to the store.
// Anything a built-in syncer can do, an external program can do identically.
package syncer

import (
	"context"
	"fmt"
	"log"
	"time"

	"todoapp/gen/task/taskconnect"
)

// DefaultInterval applies when a syncer config gives none.
const DefaultInterval = 15 * time.Minute

// Config is one syncer instance from the daemon's config file.
type Config struct {
	// Type selects the implementation (e.g. "ics").
	Type string
	// Name distinguishes instances; the task source becomes "type:name".
	Name string
	// URL or Path locates the external system, per type.
	URL      string
	Path     string
	Interval time.Duration
	// Labels applied to every task this syncer creates or updates
	// (UpsertExternalTasksRequest.apply_labels).
	Labels []string
}

// Syncer reconciles one external system into tasks.
type Syncer interface {
	// Source is the task source string this syncer owns, "type:name".
	Source() string
	// Sync performs one full reconciliation through the API.
	Sync(ctx context.Context, tc taskconnect.TaskServiceClient) error
}

// builders maps Config.Type to a constructor; implementations register in
// their init.
var builders = map[string]func(Config) (Syncer, error){}

// New builds the syncer for cfg.Type.
func New(cfg Config) (Syncer, error) {
	b, ok := builders[cfg.Type]
	if !ok {
		return nil, fmt.Errorf("unknown syncer type %q", cfg.Type)
	}
	return b(cfg)
}

// Run syncs immediately and then on every interval tick until ctx ends.
// Sync errors are logged, never fatal: the external system being down must
// not take the loop (or the daemon) down with it.
func Run(ctx context.Context, s Syncer, interval time.Duration, tc taskconnect.TaskServiceClient) {
	if interval <= 0 {
		interval = DefaultInterval
	}
	sync := func() {
		if err := s.Sync(ctx, tc); err != nil && ctx.Err() == nil {
			log.Printf("syncer %s: %v", s.Source(), err)
		}
	}
	sync()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sync()
		}
	}
}
