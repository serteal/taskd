package server

import (
	"context"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

type schemaService struct {
	taskcorev1.UnimplementedSchemaServiceServer
	s *Server
}

// ListKinds serves the merged registry view: the native kind plus every
// kind from persisted plugin manifests — including plugins that are no
// longer installed, whose items must stay renderable.
func (sc *schemaService) ListKinds(ctx context.Context, _ *taskcorev1.ListKindsRequest) (*taskcorev1.ListKindsResponse, error) {
	return &taskcorev1.ListKindsResponse{Kinds: sc.s.kinds()}, nil
}
