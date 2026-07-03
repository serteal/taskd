package server

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/store"
)

type itemService struct {
	taskcorev1.UnimplementedItemServiceServer
	s *Server
}

// errRevisionMismatch aborts a mutation when expected_todo_revision doesn't
// match; mapped to codes.Aborted.
var errRevisionMismatch = errors.New("revision mismatch")

// todoPaths is the closed set of client-writable field-mask paths. Paths are
// per-field on purpose: whole-message masks ("todo") would make partial
// updates ambiguous. todo.completed_at is server-managed.
var todoPaths = map[string]bool{
	"todo.completed":        true,
	"todo.completed_reason": true,
	"todo.labels":           true,
	"todo.project":          true,
	"todo.due":              true,
	"todo.snoozed_until":    true,
	"todo.title_override":   true,
	"todo.note":             true,
}

func (i *itemService) CreateItem(ctx context.Context, req *taskcorev1.CreateItemRequest) (*taskcorev1.CreateItemResponse, error) {
	todo := req.GetTodo()
	if todo == nil {
		return nil, status.Error(codes.InvalidArgument, "todo is required")
	}
	if strings.TrimSpace(todo.GetTitleOverride()) == "" {
		return nil, status.Error(codes.InvalidArgument, "todo.title_override (the title) is required for native tasks")
	}
	now := timestamppb.New(i.s.clk.Now())
	if todo.GetCompleted() && todo.GetCompletedAt() == nil {
		todo.CompletedAt = now
	}
	if !todo.GetCompleted() {
		todo.CompletedAt = nil
		todo.CompletedReason = ""
	}
	item := &taskcorev1.Item{
		Id:           i.s.ids.NewID(),
		Kind:         NativeKind,
		Todo:         todo,
		TodoRevision: 1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	ev, err := i.s.st.CreateItem(ctx, item, clientProvenance(ctx))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create: %v", err)
	}
	i.s.hub.Publish(ev)
	return &taskcorev1.CreateItemResponse{Item: item}, nil
}

func (i *itemService) GetItem(ctx context.Context, req *taskcorev1.GetItemRequest) (*taskcorev1.GetItemResponse, error) {
	id, err := i.resolve(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	item, err := i.s.st.GetItem(ctx, id)
	if err != nil {
		return nil, storeErr(err)
	}
	return &taskcorev1.GetItemResponse{Item: item}, nil
}

func (i *itemService) UpdateItem(ctx context.Context, req *taskcorev1.UpdateItemRequest) (*taskcorev1.UpdateItemResponse, error) {
	id, err := i.resolve(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	paths := req.GetUpdateMask().GetPaths()
	if len(paths) == 0 {
		return nil, status.Error(codes.InvalidArgument, "update_mask is required")
	}
	if req.GetItem() == nil {
		return nil, status.Error(codes.InvalidArgument, "item carrying the new values is required")
	}
	// One Update call touches one layer, by design: partial-success
	// semantics must never exist. Mirror-backed paths become intents when
	// connectors ship; until then they are read-only.
	var sawTodo, sawMirror bool
	for _, p := range paths {
		switch {
		case todoPaths[p]:
			sawTodo = true
		case p == "todo.completed_at":
			return nil, status.Error(codes.InvalidArgument, "todo.completed_at is server-managed; set todo.completed")
		case p == "mirror" || strings.HasPrefix(p, "mirror."):
			sawMirror = true
		case p == "relations":
			return nil, status.Error(codes.Unimplemented, "relations are not editable yet")
		default:
			return nil, status.Errorf(codes.InvalidArgument, "unknown or non-writable path %q", p)
		}
	}
	if sawMirror && sawTodo {
		return nil, status.Error(codes.InvalidArgument, "an update may touch only one layer (todo or mirror), never both")
	}
	if sawMirror {
		return nil, status.Error(codes.FailedPrecondition, "mirror fields are read-only: they hold remote-confirmed truth (writes become intents once connectors ship)")
	}

	src := req.GetItem().GetTodo()
	if src == nil {
		src = &taskcorev1.Todo{}
	}
	now := i.s.clk.Now()
	maskHas := func(p string) bool {
		for _, q := range paths {
			if q == p {
				return true
			}
		}
		return false
	}

	item, ev, err := i.s.st.MutateItem(ctx, id, clientProvenance(ctx), func(it *taskcorev1.Item) error {
		if exp := req.GetExpectedTodoRevision(); exp != 0 && it.GetTodoRevision() != exp {
			return errRevisionMismatch
		}
		if it.Todo == nil {
			// Promoting an un-triaged mirror to a todo.
			it.Todo = &taskcorev1.Todo{}
		}
		wasCompleted := it.Todo.GetCompleted()
		for _, p := range paths {
			applyTodoPath(it.Todo, src, p)
		}
		if it.GetKind() == NativeKind && strings.TrimSpace(it.Todo.GetTitleOverride()) == "" {
			return status.Error(codes.InvalidArgument, "a native task's title cannot be empty")
		}
		// Completion transitions are core semantics, owned here: completing
		// stamps completed_at; reopening clears it (and the reason, unless
		// the same call sets one).
		if it.Todo.GetCompleted() && !wasCompleted && it.Todo.GetCompletedAt() == nil {
			it.Todo.CompletedAt = timestamppb.New(now)
		}
		if !it.Todo.GetCompleted() && wasCompleted {
			it.Todo.CompletedAt = nil
			if !maskHas("todo.completed_reason") {
				it.Todo.CompletedReason = ""
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errRevisionMismatch) {
			return nil, status.Error(codes.Aborted, "todo_revision mismatch: item was modified concurrently; re-read and retry")
		}
		if st, ok := status.FromError(err); ok && st.Code() != codes.Unknown {
			return nil, err
		}
		return nil, storeErr(err)
	}
	if ev != nil {
		i.s.hub.Publish(ev)
	}
	return &taskcorev1.UpdateItemResponse{Item: item}, nil
}

// applyTodoPath copies one masked field from src to dst.
func applyTodoPath(dst, src *taskcorev1.Todo, path string) {
	switch path {
	case "todo.completed":
		dst.Completed = src.GetCompleted()
	case "todo.completed_reason":
		dst.CompletedReason = src.GetCompletedReason()
	case "todo.labels":
		dst.Labels = append([]string(nil), src.GetLabels()...)
	case "todo.project":
		dst.Project = src.GetProject()
	case "todo.due":
		dst.Due = cloneTS(src.GetDue())
	case "todo.snoozed_until":
		dst.SnoozedUntil = cloneTS(src.GetSnoozedUntil())
	case "todo.title_override":
		dst.TitleOverride = src.GetTitleOverride()
	case "todo.note":
		dst.Note = src.GetNote()
	}
}

func cloneTS(ts *timestamppb.Timestamp) *timestamppb.Timestamp {
	if ts == nil {
		return nil
	}
	return timestamppb.New(ts.AsTime())
}

func (i *itemService) DeleteItem(ctx context.Context, req *taskcorev1.DeleteItemRequest) (*taskcorev1.DeleteItemResponse, error) {
	id, err := i.resolve(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	item, err := i.s.st.GetItem(ctx, id)
	if err != nil {
		return nil, storeErr(err)
	}
	if item.GetKind() != NativeKind || item.GetMirror() != nil {
		return nil, status.Error(codes.FailedPrecondition, "only native tasks can be deleted; tracked items are completed, never deleted")
	}
	ev, err := i.s.st.DeleteItem(ctx, id, clientProvenance(ctx))
	if err != nil {
		return nil, storeErr(err)
	}
	i.s.hub.Publish(ev)
	return &taskcorev1.DeleteItemResponse{}, nil
}

func (i *itemService) QueryItems(ctx context.Context, req *taskcorev1.QueryItemsRequest) (*taskcorev1.QueryItemsResponse, error) {
	compiled, err := i.s.eng.Compile(req.GetFilter())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "filter: %v", err)
	}
	orderBy, desc, err := parseOrderBy(req.GetOrderBy())
	if err != nil {
		return nil, err
	}
	res, err := i.s.st.QueryItems(ctx, store.Query{
		Where:     compiled.Where,
		Args:      compiled.Args,
		Residual:  compiled.Residual,
		OrderBy:   orderBy,
		Desc:      desc,
		PageSize:  int(req.GetPageSize()),
		PageToken: req.GetPageToken(),
	})
	if err != nil {
		return nil, storeErr(err)
	}
	return &taskcorev1.QueryItemsResponse{
		Items:         res.Items,
		NextPageToken: res.NextPageToken,
		Cursor:        res.Cursor,
	}, nil
}

var orderColumns = map[string]bool{
	"due": true, "created_at": true, "updated_at": true, "project": true, "kind": true,
}

func parseOrderBy(s string) (col string, desc bool, err error) {
	fields := strings.Fields(strings.ToLower(s))
	switch len(fields) {
	case 0:
		return "created_at", true, nil
	case 1:
		col = fields[0]
	case 2:
		col = fields[0]
		switch fields[1] {
		case "desc":
			desc = true
		case "asc":
		default:
			return "", false, status.Errorf(codes.InvalidArgument, "order_by direction %q (want asc or desc)", fields[1])
		}
	default:
		return "", false, status.Errorf(codes.InvalidArgument, "malformed order_by %q", s)
	}
	if !orderColumns[col] {
		return "", false, status.Errorf(codes.InvalidArgument, "order_by column %q (want one of due, created_at, updated_at, project, kind)", col)
	}
	return col, desc, nil
}

// resolve expands id prefixes; all item RPCs accept a unique prefix.
func (i *itemService) resolve(ctx context.Context, id string) (string, error) {
	if id == "" {
		return "", status.Error(codes.InvalidArgument, "id is required")
	}
	full, err := i.s.st.ResolveIDPrefix(ctx, id)
	if err != nil {
		return "", storeErr(err)
	}
	return full, nil
}

func storeErr(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return status.Error(codes.NotFound, "not found")
	case errors.Is(err, store.ErrAmbiguousPrefix):
		return status.Error(codes.InvalidArgument, "id prefix is ambiguous; give more characters")
	case errors.Is(err, store.ErrInvalidPageToken):
		return status.Error(codes.InvalidArgument, "invalid page token")
	default:
		return status.Errorf(codes.Internal, "%v", err)
	}
}
