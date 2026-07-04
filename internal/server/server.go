// Package server implements TaskService over the store: request validation,
// error mapping, and change fan-out to watchers. It contains no domain logic
// of its own — semantics live in the store, transport in the daemon.
package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"connectrpc.com/connect"

	taskpb "todoapp/gen/task"
	"todoapp/gen/task/taskconnect"
	"todoapp/internal/store"
)

// updatablePaths are the UpdateTask mask paths; everything else on Task is
// server-assigned (id, revision, timestamps) or syncer-owned (source,
// external_ref, external_data) and is rejected, not ignored.
var updatablePaths = map[string]struct{}{
	"title":          {},
	"notes":          {},
	"labels":         {},
	"due_time":       {},
	"completed_time": {},
	"user_data":      {},
}

type Server struct {
	st  *store.Store
	hub *Hub
}

func New(st *store.Store) *Server {
	return &Server{st: st, hub: NewHub()}
}

// Handler returns the mount path and HTTP handler for the service.
func (s *Server) Handler() (string, http.Handler) {
	return taskconnect.NewTaskServiceHandler(s)
}

var _ taskconnect.TaskServiceHandler = (*Server)(nil)

func (s *Server) CreateTask(ctx context.Context, req *connect.Request[taskpb.CreateTaskRequest]) (*connect.Response[taskpb.CreateTaskResponse], error) {
	t, err := s.st.Create(ctx, req.Msg.GetTitle(), req.Msg.GetNotes(), req.Msg.GetLabels(), req.Msg.GetDueTime())
	if err != nil {
		return nil, mapErr(err)
	}
	s.hub.PublishTask(t)
	return connect.NewResponse(&taskpb.CreateTaskResponse{Task: t}), nil
}

func (s *Server) GetTask(ctx context.Context, req *connect.Request[taskpb.GetTaskRequest]) (*connect.Response[taskpb.GetTaskResponse], error) {
	if req.Msg.GetId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("id is required"))
	}
	t, err := s.st.Get(ctx, req.Msg.GetId())
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&taskpb.GetTaskResponse{Task: t}), nil
}

func (s *Server) UpdateTask(ctx context.Context, req *connect.Request[taskpb.UpdateTaskRequest]) (*connect.Response[taskpb.UpdateTaskResponse], error) {
	msg := req.Msg
	if msg.GetId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("id is required"))
	}
	paths := msg.GetUpdateMask().GetPaths()
	if len(paths) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("update_mask is required"))
	}
	for _, p := range paths {
		if _, ok := updatablePaths[p]; !ok {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("path %q is not updatable (updatable: title, notes, labels, due_time, completed_time, user_data)", p))
		}
	}
	src := msg.GetTask()
	t, err := s.st.Update(ctx, msg.GetId(), msg.GetExpectedRevision(), func(t *taskpb.Task) error {
		for _, p := range paths {
			switch p {
			case "title":
				t.Title = src.GetTitle()
			case "notes":
				t.Notes = src.GetNotes()
			case "labels":
				t.Labels = src.GetLabels()
			case "due_time":
				t.DueTime = src.GetDueTime()
			case "completed_time":
				t.CompletedTime = src.GetCompletedTime()
			case "user_data":
				t.UserData = src.GetUserData()
			}
		}
		return nil
	})
	if err != nil {
		return nil, mapErr(err)
	}
	s.hub.PublishTask(t)
	return connect.NewResponse(&taskpb.UpdateTaskResponse{Task: t}), nil
}

func (s *Server) DeleteTask(ctx context.Context, req *connect.Request[taskpb.DeleteTaskRequest]) (*connect.Response[taskpb.DeleteTaskResponse], error) {
	if req.Msg.GetId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("id is required"))
	}
	if err := s.st.Delete(ctx, req.Msg.GetId()); err != nil {
		return nil, mapErr(err)
	}
	s.hub.PublishDeleted(req.Msg.GetId())
	return connect.NewResponse(&taskpb.DeleteTaskResponse{}), nil
}

func (s *Server) ListTasks(ctx context.Context, req *connect.Request[taskpb.ListTasksRequest]) (*connect.Response[taskpb.ListTasksResponse], error) {
	msg := req.Msg
	tasks, next, err := s.st.List(ctx, store.Page{
		Filter:    msg.GetFilter(),
		OrderBy:   msg.GetOrderBy(),
		PageSize:  msg.GetPageSize(),
		PageToken: msg.GetPageToken(),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&taskpb.ListTasksResponse{Tasks: tasks, NextPageToken: next}), nil
}

func (s *Server) UpsertExternalTasks(ctx context.Context, req *connect.Request[taskpb.UpsertExternalTasksRequest]) (*connect.Response[taskpb.UpsertExternalTasksResponse], error) {
	msg := req.Msg
	res, err := s.st.UpsertExternal(ctx, msg.GetSource(), msg.GetTasks(), msg.GetApplyLabels(), msg.GetFullSnapshot())
	if err != nil {
		return nil, mapErr(err)
	}
	for _, t := range res.Changed {
		s.hub.PublishTask(t)
	}
	for _, id := range res.DeletedIDs {
		s.hub.PublishDeleted(id)
	}
	return connect.NewResponse(&taskpb.UpsertExternalTasksResponse{
		Created:   res.Created,
		Updated:   res.Updated,
		Unchanged: res.Unchanged,
		Deleted:   res.Deleted,
	}), nil
}

func (s *Server) WatchTasks(ctx context.Context, _ *connect.Request[taskpb.WatchTasksRequest], stream *connect.ServerStream[taskpb.WatchTasksResponse]) error {
	ch, cancel := s.hub.Subscribe()
	defer cancel()
	// Empty handshake, sent after subscribing: it flushes response headers
	// (without it the client's stream-open call blocks until the first real
	// event) and tells the client the subscription is live, so a ListTasks
	// issued after it cannot miss changes.
	if err := stream.Send(&taskpb.WatchTasksResponse{}); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-ch:
			if !ok {
				return connect.NewError(connect.CodeResourceExhausted,
					errors.New("watcher fell behind; reconnect and refetch"))
			}
			if err := stream.Send(ev); err != nil {
				return err
			}
		}
	}
}

func (s *Server) ListLabels(ctx context.Context, req *connect.Request[taskpb.ListLabelsRequest]) (*connect.Response[taskpb.ListLabelsResponse], error) {
	labels, err := s.st.ListLabels(ctx, req.Msg.GetIncludeCompleted())
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&taskpb.ListLabelsResponse{Labels: labels}), nil
}

// mapErr translates store sentinels into Connect codes.
func mapErr(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, store.ErrRevisionMismatch):
		return connect.NewError(connect.CodeAborted, err)
	case errors.Is(err, store.ErrInvalid):
		return connect.NewError(connect.CodeInvalidArgument, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}
