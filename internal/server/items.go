package server

import (
	"context"
	"errors"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/contrib"
	"todoapp/internal/intent"
	"todoapp/internal/store"
	tasksync "todoapp/internal/sync"
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
		// One write API, two delivery paths: the only mirror-backed field
		// writable by mask is the title, and "writing" it means asking the
		// remote to rename — the mirror itself only ever holds
		// remote-confirmed truth.
		if len(paths) != 1 || paths[0] != "mirror.title" {
			return nil, status.Error(codes.FailedPrecondition,
				"mirror fields are read-only except mirror.title (which routes to the connector as a rename intent)")
		}
		return i.renameViaIntent(ctx, id, req.GetItem().GetMirror().GetTitle())
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

// renameViaIntent routes an UpdateItem on mirror.title through the outbox
// with the bounded wait: CONFIRMED within ~2s when the connector is
// responsive, else the QUEUED record rides back in the response.
func (i *itemService) renameViaIntent(ctx context.Context, id, title string) (*taskcorev1.UpdateItemResponse, error) {
	if i.s.intents == nil {
		return nil, status.Error(codes.Unavailable, "intent router not running")
	}
	if strings.TrimSpace(title) == "" {
		return nil, status.Error(codes.InvalidArgument, "mirror.title cannot be renamed to empty")
	}
	params, err := structpb.NewStruct(map[string]any{"title": title})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "params: %v", err)
	}
	rec, err := i.s.intents.Invoke(ctx, id, "rename", params, 2*time.Second)
	if err != nil {
		return nil, intentErr(err)
	}
	item, gerr := i.s.st.GetItem(ctx, id)
	if gerr != nil {
		return nil, storeErr(gerr)
	}
	return &taskcorev1.UpdateItemResponse{Item: item, Intent: rec}, nil
}

// intentErr maps the router's typed errors to gRPC codes.
func intentErr(err error) error {
	switch {
	case errors.Is(err, intent.ErrNotMirrored):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, intent.ErrUnknownIntent), errors.Is(err, intent.ErrInvalidParams):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, intent.ErrUnsupported):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, intent.ErrNotRetryable), errors.Is(err, intent.ErrNotDiscardable):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return storeErr(err)
	}
}

// LinkItem: attach-to-remote. Resolves the reference through the connector
// and pins the mirror — the one sanctioned kind change in the system (a
// native task becomes a tracked item).
func (i *itemService) LinkItem(ctx context.Context, req *taskcorev1.LinkItemRequest) (*taskcorev1.LinkItemResponse, error) {
	id, err := i.resolve(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	if i.s.dispatch == nil {
		return nil, status.Error(codes.Unavailable, "no connector instances running")
	}
	if req.GetConnectorInstance() == "" || req.GetRef() == "" {
		return nil, status.Error(codes.InvalidArgument, "connector_instance and ref are required")
	}
	item, err := i.s.st.GetItem(ctx, id)
	if err != nil {
		return nil, storeErr(err)
	}
	if item.GetMirror() != nil {
		return nil, status.Error(codes.FailedPrecondition, "item already tracks a remote; an item has at most one")
	}
	remote, err := i.s.dispatch.Resolve(ctx, req.GetConnectorInstance(), req.GetRef())
	if err != nil {
		if st, ok := status.FromError(err); ok {
			return nil, status.Errorf(st.Code(), "resolving %q via %s: %s", req.GetRef(), req.GetConnectorInstance(), st.Message())
		}
		return nil, status.Errorf(codes.Internal, "resolve: %v", err)
	}
	if remote.GetExternalId() == "" || remote.GetKind() == "" {
		return nil, status.Error(codes.Internal, "connector resolved an item without identity")
	}
	if existing, err := i.s.st.GetItemByExternal(ctx, req.GetConnectorInstance(), remote.GetExternalId()); err == nil {
		return nil, status.Errorf(codes.AlreadyExists, "that remote object is already tracked as %s", existing.GetId())
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, storeErr(err)
	}

	now := i.s.clk.Now()
	prov := &taskcorev1.Provenance{Source: taskcorev1.ProvenanceSource_PROVENANCE_SOURCE_SYNC, Ref: req.GetConnectorInstance()}
	updated, evt, err := i.s.st.MutateItem(ctx, id, prov, func(it *taskcorev1.Item) error {
		it.Kind = remote.GetKind()
		it.Mirror = &taskcorev1.Mirror{
			Pinned: true,
			Link: &taskcorev1.ExternalLink{
				ConnectorInstance: req.GetConnectorInstance(),
				ExternalId:        remote.GetExternalId(),
			},
		}
		tasksync.ApplyRemote(it.Mirror, remote, now)
		return nil
	})
	if err != nil {
		return nil, storeErr(err)
	}
	if evt != nil {
		i.s.hub.Publish(evt)
	}
	return &taskcorev1.LinkItemResponse{Item: updated}, nil
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
	resp := &taskcorev1.QueryItemsResponse{
		Items:         res.Items,
		NextPageToken: res.NextPageToken,
		Cursor:        res.Cursor,
	}
	if req.GetRender() != nil {
		cols := contrib.Columns(req.GetRender())
		resp.RenderColumns = cols
		resp.Rows = i.s.render.Render(res.Items, cols, i.s.clk.Now())
	}
	return resp, nil
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
