package taskplugin_test

import (
	"context"
	"net"
	"path/filepath"
	"sync"
	"testing"

	"google.golang.org/grpc"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
	"todoapp/pkg/taskplugin"
)

// memSecrets is a SecretService test double standing in for the core host.
type memSecrets struct {
	pluginv1.UnimplementedSecretServiceServer
	mu sync.Mutex
	m  map[string][]byte
}

func (s *memSecrets) PutSecret(_ context.Context, req *pluginv1.PutSecretRequest) (*pluginv1.PutSecretResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = make(map[string][]byte)
	}
	s.m[req.GetKey()] = req.GetValue()
	return &pluginv1.PutSecretResponse{}, nil
}

func (s *memSecrets) GetSecret(_ context.Context, req *pluginv1.GetSecretRequest) (*pluginv1.GetSecretResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[req.GetKey()]
	return &pluginv1.GetSecretResponse{Found: ok, Value: v}, nil
}

func TestCoreHost(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "h")
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	pluginv1.RegisterSecretServiceServer(srv, &memSecrets{})
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)

	t.Setenv(taskplugin.CoreHostSocketEnv, sock)
	ctx := context.Background()
	secrets, closer, err := taskplugin.DialCoreHost(ctx)
	if err != nil {
		t.Fatalf("DialCoreHost: %v", err)
	}
	defer closer.Close()

	if _, err := secrets.PutSecret(ctx, &pluginv1.PutSecretRequest{Key: "token", Value: []byte("hunter2")}); err != nil {
		t.Fatalf("PutSecret: %v", err)
	}
	got, err := secrets.GetSecret(ctx, &pluginv1.GetSecretRequest{Key: "token"})
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if !got.GetFound() || string(got.GetValue()) != "hunter2" {
		t.Errorf("GetSecret = (found=%v, %q), want (true, hunter2)", got.GetFound(), got.GetValue())
	}
	miss, err := secrets.GetSecret(ctx, &pluginv1.GetSecretRequest{Key: "nope"})
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if miss.GetFound() {
		t.Error("GetSecret(nope).found = true, want false")
	}
}

func TestCoreHostNoEnv(t *testing.T) {
	t.Setenv(taskplugin.CoreHostSocketEnv, "")
	_, _, err := taskplugin.DialCoreHost(context.Background())
	if err == nil {
		t.Fatal("DialCoreHost without TASKPLUGIN_COREHOST_SOCKET succeeded, want error")
	}
}
