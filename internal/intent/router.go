// Package intent is the write path toward remotes: the router turns
// semantic commands into durable outbox records, the worker delivers them
// through connectors, and confirmed remote state lands on the mirror.
// Intents either succeed or fail loudly; there is no three-way merge. User
// edits, rule write-backs, and agent writes all pass through here — one
// place for capability checks, provenance, and audit.
package intent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
	taskcorev1 "todoapp/gen/taskcore/v1"
)

// Dispatcher is the live-connector surface (implemented by
// internal/plugin.Registry; faked in tests).
type Dispatcher interface {
	Supports(instance, kind string, in pluginv1.Intent) bool
	HandleIntent(ctx context.Context, instance string, req *pluginv1.HandleIntentRequest) (*pluginv1.RemoteItem, error)
	Resolve(ctx context.Context, instance, ref string) (*pluginv1.RemoteItem, error)
}

// Typed errors the server maps to gRPC codes.
var (
	ErrNotMirrored    = errors.New("item has no remote: intents only apply to tracked items")
	ErrUnknownIntent  = errors.New("unknown intent name")
	ErrUnsupported    = errors.New("connector does not handle this intent for this kind")
	ErrInvalidParams  = errors.New("invalid intent params")
	ErrNotRetryable   = errors.New("only FAILED intents can be retried")
	ErrNotDiscardable = errors.New("only QUEUED or FAILED intents can be discarded")
)

// names maps the wire-stable lowercase intent names to the plugin enum.
var names = map[string]pluginv1.Intent{
	"rename":        pluginv1.Intent_INTENT_RENAME,
	"add_comment":   pluginv1.Intent_INTENT_ADD_COMMENT,
	"delete":        pluginv1.Intent_INTENT_DELETE,
	"set_due":       pluginv1.Intent_INTENT_SET_DUE,
	"set_start":     pluginv1.Intent_INTENT_SET_START,
	"assign":        pluginv1.Intent_INTENT_ASSIGN,
	"set_priority":  pluginv1.Intent_INTENT_SET_PRIORITY,
	"set_completed": pluginv1.Intent_INTENT_SET_COMPLETED,
}

// Known reports whether name is a standard intent (rules.Validate uses it).
func Known(name string) bool {
	_, ok := names[strings.ToLower(name)]
	return ok
}

// deriveParams fills facet-intent parameters from the item's current local
// state when the caller sent none — rule write-back pushes what the item
// now says.
func deriveParams(name string, item *taskcorev1.Item) (*structpb.Struct, error) {
	todo := item.GetTodo()
	switch name {
	case "set_completed":
		fields := map[string]any{"completed": todo.GetCompleted()}
		if r := todo.GetCompletedReason(); r != "" {
			fields["reason"] = r
		}
		return structpb.NewStruct(fields)
	case "set_due":
		if d := todo.GetDue(); d != nil {
			return structpb.NewStruct(map[string]any{"due": d.AsTime().Format(time.RFC3339)})
		}
		return structpb.NewStruct(map[string]any{"due": nil})
	default:
		return nil, fmt.Errorf("%w: intent %q requires explicit params", ErrInvalidParams, name)
	}
}

func nowTS(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t) }
