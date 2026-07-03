// Package server implements the taskcore.v1 gRPC services on top of the
// store, feed hub, and query engine. It owns all API semantics: layer
// routing on writes, completion transitions, cursor expiry, and provenance.
package server

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/clock"
	"todoapp/internal/feed"
	"todoapp/internal/query"
	"todoapp/internal/store"
)

// Version is the daemon version, overridable at build time via
// -ldflags "-X todoapp/internal/server.Version=...".
var Version = "0.1.0-dev"

// NativeKind is the kind of items created directly by users.
const NativeKind = "task"

type Server struct {
	st  store.Store
	hub *feed.Hub
	eng *query.Engine
	clk clock.Clock
	ids clock.IDGen
}

func New(st store.Store, hub *feed.Hub, eng *query.Engine, clk clock.Clock, ids clock.IDGen) *Server {
	return &Server{st: st, hub: hub, eng: eng, clk: clk, ids: ids}
}

// Register attaches all taskcore.v1 services to g.
func (s *Server) Register(g *grpc.Server) {
	taskcorev1.RegisterItemServiceServer(g, &itemService{s: s})
	taskcorev1.RegisterViewServiceServer(g, &viewService{s: s})
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
