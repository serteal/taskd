// Package sync is the reconciliation engine: it turns connector snapshots
// into item creates/updates, missing marks, and stale marks — the smart half
// of the dumb-connector/smart-core split. Connectors enumerate and
// translate; everything here is written once, for every connector.
package sync

import (
	"context"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
)

// Source is one configured connector instance as the engine sees it —
// implemented by internal/plugin's gRPC adapter, and by in-memory fakes in
// tests. Snapshot returns the full current in-scope remote set; an error
// means "remote unavailable this cycle" (the engine logs and retries next
// cycle; it never treats an errored snapshot as an empty one, which would
// mass-tombstone the instance).
type Source interface {
	// Instance is the connector instance name, e.g. "cal@personal" — the
	// link namespace for everything this source yields.
	Instance() string
	Snapshot(ctx context.Context) ([]*pluginv1.RemoteItem, error)
	// Resolve fetches one remote object by connector-native reference —
	// pinned (attached) mirrors refresh through here, since they may live
	// outside the instance's snapshot scope.
	Resolve(ctx context.Context, ref string) (*pluginv1.RemoteItem, error)
}
