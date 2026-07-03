package server

import (
	"context"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

type viewService struct {
	taskcorev1.UnimplementedViewServiceServer
	s *Server
}

func (v *viewService) SaveView(ctx context.Context, req *taskcorev1.SaveViewRequest) (*taskcorev1.SaveViewResponse, error) {
	view := req.GetView()
	if view == nil || strings.TrimSpace(view.GetName()) == "" {
		return nil, status.Error(codes.InvalidArgument, "view.name is required")
	}
	// Validate expressions at save time so a broken view fails here, not at
	// every later query.
	if view.GetFilter() != "" {
		if _, err := v.s.eng.Compile(view.GetFilter()); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "view filter: %v", err)
		}
	}
	if view.GetOrderBy() != "" {
		if _, _, err := parseOrderBy(view.GetOrderBy()); err != nil {
			return nil, err
		}
	}
	if err := v.s.st.SaveView(ctx, view); err != nil {
		return nil, storeErr(err)
	}
	return &taskcorev1.SaveViewResponse{View: view}, nil
}

func (v *viewService) GetView(ctx context.Context, req *taskcorev1.GetViewRequest) (*taskcorev1.GetViewResponse, error) {
	view, err := v.s.st.GetView(ctx, req.GetName())
	if err != nil {
		return nil, storeErr(err)
	}
	return &taskcorev1.GetViewResponse{View: view}, nil
}

func (v *viewService) ListViews(ctx context.Context, _ *taskcorev1.ListViewsRequest) (*taskcorev1.ListViewsResponse, error) {
	views, err := v.s.st.ListViews(ctx)
	if err != nil {
		return nil, storeErr(err)
	}
	return &taskcorev1.ListViewsResponse{Views: views}, nil
}

func (v *viewService) DeleteView(ctx context.Context, req *taskcorev1.DeleteViewRequest) (*taskcorev1.DeleteViewResponse, error) {
	if err := v.s.st.DeleteView(ctx, req.GetName()); err != nil {
		return nil, storeErr(err)
	}
	return &taskcorev1.DeleteViewResponse{}, nil
}
