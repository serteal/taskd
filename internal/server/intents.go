package server

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

// intentService exposes the outbox router (internal/intent.Router) over gRPC:
// invoke writes toward remotes, and inspect/retry/discard what is queued or
// failed. All write policy — capability checks, provenance, derivation of
// facet params — lives in the router; this layer resolves id prefixes and
// maps typed errors to gRPC codes (via storeErr/intentErr in items.go).
type intentService struct {
	taskcorev1.UnimplementedIntentServiceServer
	s *Server
}

// InvokeIntent resolves the item id prefix, then routes one intent with the
// standard bounded wait: CONFIRMED within ~2s when the connector is
// responsive, else the QUEUED record rides back and the outbox delivers.
func (x *intentService) InvokeIntent(ctx context.Context, req *taskcorev1.InvokeIntentRequest) (*taskcorev1.InvokeIntentResponse, error) {
	if x.s.intents == nil {
		return nil, status.Error(codes.Unavailable, "intent router not running")
	}
	if req.GetItemId() == "" {
		return nil, status.Error(codes.InvalidArgument, "item_id is required")
	}
	full, err := x.s.st.ResolveIDPrefix(ctx, req.GetItemId())
	if err != nil {
		return nil, storeErr(err)
	}
	rec, err := x.s.intents.Invoke(ctx, full, req.GetIntent(), req.GetParams(), 2*time.Second)
	if err != nil {
		return nil, intentErr(err)
	}
	return &taskcorev1.InvokeIntentResponse{Record: rec}, nil
}

// GetIntent reads one outbox record by id. Intent ids are ULIDs, but there is
// no prefix resolver for intents — they are addressed by the EXACT id the CLI
// already holds from its own `task pending` listing, so no prefix expansion is
// attempted here.
func (x *intentService) GetIntent(ctx context.Context, req *taskcorev1.GetIntentRequest) (*taskcorev1.GetIntentResponse, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	rec, err := x.s.st.GetIntent(ctx, req.GetId())
	if err != nil {
		return nil, storeErr(err)
	}
	return &taskcorev1.GetIntentResponse{Record: rec}, nil
}

// ListIntents is a store passthrough: states and limit go straight through
// (empty states means the live outbox — QUEUED, INFLIGHT, FAILED), and the
// optional item_id filter resolves as a prefix when non-empty.
func (x *intentService) ListIntents(ctx context.Context, req *taskcorev1.ListIntentsRequest) (*taskcorev1.ListIntentsResponse, error) {
	itemID := req.GetItemId()
	if itemID != "" {
		full, err := x.s.st.ResolveIDPrefix(ctx, itemID)
		if err != nil {
			return nil, storeErr(err)
		}
		itemID = full
	}
	recs, err := x.s.st.ListIntents(ctx, req.GetStates(), itemID, int(req.GetLimit()))
	if err != nil {
		return nil, storeErr(err)
	}
	return &taskcorev1.ListIntentsResponse{Records: recs}, nil
}

// RetryIntent re-queues a FAILED intent for immediate delivery.
func (x *intentService) RetryIntent(ctx context.Context, req *taskcorev1.RetryIntentRequest) (*taskcorev1.RetryIntentResponse, error) {
	if x.s.intents == nil {
		return nil, status.Error(codes.Unavailable, "intent router not running")
	}
	rec, err := x.s.intents.Retry(ctx, req.GetId())
	if err != nil {
		return nil, intentErr(err)
	}
	return &taskcorev1.RetryIntentResponse{Record: rec}, nil
}

// DiscardIntent is the terminal user decision for a QUEUED or FAILED intent.
func (x *intentService) DiscardIntent(ctx context.Context, req *taskcorev1.DiscardIntentRequest) (*taskcorev1.DiscardIntentResponse, error) {
	if x.s.intents == nil {
		return nil, status.Error(codes.Unavailable, "intent router not running")
	}
	rec, err := x.s.intents.Discard(ctx, req.GetId())
	if err != nil {
		return nil, intentErr(err)
	}
	return &taskcorev1.DiscardIntentResponse{Record: rec}, nil
}
