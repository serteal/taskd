// Package server implements the taskcore.v1 gRPC services on top of the
// store, feed hub, and query engine. It owns all API semantics: layer
// routing on writes, completion transitions, cursor expiry, and provenance.
package server

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/clock"
	"todoapp/internal/contrib"
	"todoapp/internal/feed"
	"todoapp/internal/intent"
	"todoapp/internal/query"
	"todoapp/internal/store"
)

// Version is the daemon version, overridable at build time via
// -ldflags "-X todoapp/internal/server.Version=...".
var Version = "0.1.0-dev"

// NativeKind is the kind of items created directly by users.
const NativeKind = "task"

// Options wires the server's collaborators. Store, Hub, Engine, Clock, and
// IDs are required; the rest degrade gracefully when absent (tests wire
// only what they exercise).
type Options struct {
	Store store.Store
	Hub   *feed.Hub
	Eng   *query.Engine
	Clock clock.Clock
	IDs   clock.IDGen
	// Kinds is the schema registry's merged view (SchemaService).
	Kinds func() []*taskcorev1.KindInfo
	// Backfill is the rules engine's explicit level-apply (RuleService).
	Backfill func(ctx context.Context, name string) (int, error)
	// Intents is the outbox router (IntentService + mirror-path updates).
	Intents *intent.Router
	// Dispatch resolves remote refs for LinkItem (the plugin registry).
	Dispatch intent.Dispatcher
	// Render is the contribution renderer (core-side display rows).
	Render *contrib.Renderer
}

type Server struct {
	st       store.Store
	hub      *feed.Hub
	eng      *query.Engine
	clk      clock.Clock
	ids      clock.IDGen
	kinds    func() []*taskcorev1.KindInfo
	backfill func(ctx context.Context, name string) (int, error)
	intents  *intent.Router
	dispatch intent.Dispatcher
	render   *contrib.Renderer
}

func New(o Options) *Server {
	if o.Kinds == nil {
		o.Kinds = func() []*taskcorev1.KindInfo { return nil }
	}
	if o.Backfill == nil {
		o.Backfill = func(context.Context, string) (int, error) {
			return 0, errors.New("rules engine not running")
		}
	}
	if o.Render == nil {
		o.Render = contrib.NewRenderer(o.Eng)
	}
	return &Server{
		st: o.Store, hub: o.Hub, eng: o.Eng, clk: o.Clock, ids: o.IDs,
		kinds: o.Kinds, backfill: o.Backfill, intents: o.Intents, dispatch: o.Dispatch,
		render: o.Render,
	}
}

// Register attaches all taskcore.v1 services to g.
func (s *Server) Register(g *grpc.Server) {
	taskcorev1.RegisterItemServiceServer(g, &itemService{s: s})
	taskcorev1.RegisterViewServiceServer(g, &viewService{s: s})
	taskcorev1.RegisterRuleServiceServer(g, &ruleService{s: s})
	taskcorev1.RegisterIntentServiceServer(g, &intentService{s: s})
	taskcorev1.RegisterSchemaServiceServer(g, &schemaService{s: s})
	taskcorev1.RegisterAdminServiceServer(g, &adminService{s: s})
}

// clientProvenance builds the provenance for a write arriving over the
// client API. The client self-identifies via the x-task-client metadata
// header; provenance is an audit trail, not a security boundary.
func clientProvenance(ctx context.Context) *taskcorev1.Provenance {
	name := "unknown"
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := md.Get("x-task-client"); len(v) > 0 && v[0] != "" {
			name = v[0]
		}
	}
	return &taskcorev1.Provenance{
		Source: taskcorev1.ProvenanceSource_PROVENANCE_SOURCE_CLIENT,
		Ref:    name,
	}
}
