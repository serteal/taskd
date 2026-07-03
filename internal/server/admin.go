package server

import (
	"context"
	"path/filepath"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

type adminService struct {
	taskcorev1.UnimplementedAdminServiceServer
	s *Server
}

func (a *adminService) Ping(ctx context.Context, _ *taskcorev1.PingRequest) (*taskcorev1.PingResponse, error) {
	oldest, err := a.s.st.OldestCursor(ctx)
	if err != nil {
		return nil, storeErr(err)
	}
	latest, err := a.s.st.LatestCursor(ctx)
	if err != nil {
		return nil, storeErr(err)
	}
	return &taskcorev1.PingResponse{
		DaemonVersion: Version,
		OldestCursor:  oldest,
		LatestCursor:  latest,
	}, nil
}

func (a *adminService) Backup(ctx context.Context, req *taskcorev1.BackupRequest) (*taskcorev1.BackupResponse, error) {
	dest := req.GetDestinationPath()
	if !filepath.IsAbs(dest) {
		return nil, status.Error(codes.InvalidArgument, "destination_path must be absolute (it is interpreted by the daemon, not the client)")
	}
	n, err := a.s.st.Backup(ctx, dest)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "backup: %v", err)
	}
	return &taskcorev1.BackupResponse{BytesWritten: n}, nil
}
