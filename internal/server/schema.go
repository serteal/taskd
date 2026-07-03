package server

import (
	"context"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

type schemaService struct {
	taskcorev1.UnimplementedSchemaServiceServer
	s *Server
}

// Phase 1 registers only the native kind, statically. When the plugin host
// ships, this becomes a read from the same registry the query engine's
// RegisterKind feeds, so the two can never disagree.
func (sc *schemaService) ListKinds(ctx context.Context, _ *taskcorev1.ListKindsRequest) (*taskcorev1.ListKindsResponse, error) {
	return &taskcorev1.ListKindsResponse{
		Kinds: []*taskcorev1.KindInfo{
			{
				Kind: NativeKind,
				Facets: []*taskcorev1.FacetBinding{
					{Facet: "completable"},
					{Facet: "schedulable", Bindings: map[string]string{"due": "item.todo.due"}},
				},
			},
		},
	}, nil
}
