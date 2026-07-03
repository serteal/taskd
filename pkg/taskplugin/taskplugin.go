// Package taskplugin is the public Go SDK for plugin authors (DESIGN.md
// §10). A plugin is an ordinary executable: implement Connector, call Serve
// with a Manifest, and the task host does the rest — it launches one process
// per connector instance, hands it a unix socket via TASKPLUGIN_SOCKET, and
// calls GetManifest and Configure exactly once each.
//
// Connectors are dumb by design: they translate between the remote's domain
// model and RemoteItems and own external identity. Reconciliation, diffing,
// tombstone grace, and scheduling belong to the core and never leak in here.
package taskplugin

import (
	"context"

	"google.golang.org/protobuf/types/known/structpb"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
)

// Connector is what a plugin author implements: the minimal source surface,
// SNAPSHOT enumeration only. Optional capabilities (Resolve, HandleIntent)
// are added by also implementing Resolver or IntentHandler; Serve discovers
// them by interface assertion.
type Connector interface {
	// Configure binds the process to one named instance with its config.
	// Called exactly once per process, before any other connector call.
	// Reject bad config with codes.InvalidArgument. Returned capabilities
	// may depend on the config.
	Configure(ctx context.Context, instance string, config *structpb.Struct) (*pluginv1.Capabilities, error)
	// Snapshot enumerates the full current in-scope set, calling emit once
	// per remote object. The core diffs consecutive snapshots; emit items in
	// a stable order with stable external_ids so unchanged remotes produce
	// byte-identical snapshots (the core's no-op suppression depends on it).
	Snapshot(ctx context.Context, emit func(*pluginv1.RemoteItem) error) error
}

// Resolver is the optional attach-to-remote capability: fetch one remote
// object by user-supplied reference (URL or native handle), even outside the
// instance's configured scope.
type Resolver interface {
	Resolve(ctx context.Context, ref string) (*pluginv1.RemoteItem, error)
}

// IntentHandler is the optional write capability: perform one standard
// intent against the remote and return the remote-confirmed result. The core
// routes only intents declared in Capabilities; read-only connectors simply
// don't implement this.
type IntentHandler interface {
	HandleIntent(ctx context.Context, req *pluginv1.HandleIntentRequest) (*pluginv1.RemoteItem, error)
}
