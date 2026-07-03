package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
	tasksync "todoapp/internal/sync"
)

// Source returns the sync.Source adapter over this instance's
// ConnectorService. It survives restarts: calls route through the current
// client under the lock, and while the plugin is down Snapshot returns an
// error — the engine logs and retries next cycle (never treats it as empty).
func (r *Running) Source() tasksync.Source {
	return &source{r: r}
}

type source struct {
	r *Running
}

func (s *source) Instance() string { return s.r.inst.Name }

func (s *source) Snapshot(ctx context.Context) ([]*pluginv1.RemoteItem, error) {
	s.r.mu.Lock()
	c := s.r.connector
	s.r.mu.Unlock()
	if c == nil {
		return nil, fmt.Errorf("plugin %s: not running", s.r.inst.Name)
	}
	stream, err := c.Snapshot(ctx, &pluginv1.SnapshotRequest{})
	if err != nil {
		return nil, fmt.Errorf("plugin %s: snapshot: %w", s.r.inst.Name, err)
	}
	var items []*pluginv1.RemoteItem
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return items, nil
		}
		if err != nil {
			return nil, fmt.Errorf("plugin %s: snapshot recv: %w", s.r.inst.Name, err)
		}
		if it := resp.GetItem(); it != nil {
			items = append(items, it)
		}
	}
}
