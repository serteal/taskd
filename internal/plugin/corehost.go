package plugin

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
	"todoapp/internal/secret"
)

// secretServer serves pluginv1.SecretService on one instance's corehost
// socket. Keys are namespaced "<instance>/<key>" into the host's store, so
// an instance can never read secrets it didn't store — least privilege by
// construction, not by plugin good manners.
type secretServer struct {
	pluginv1.UnimplementedSecretServiceServer
	ns    string
	store secret.Store
}

func (s *secretServer) key(k string) string { return s.ns + "/" + k }

func (s *secretServer) check(k string) error {
	if k == "" {
		return status.Error(codes.InvalidArgument, "secret key required")
	}
	if s.store == nil {
		return status.Error(codes.FailedPrecondition, "host has no secret store")
	}
	return nil
}

func (s *secretServer) PutSecret(_ context.Context, req *pluginv1.PutSecretRequest) (*pluginv1.PutSecretResponse, error) {
	if err := s.check(req.GetKey()); err != nil {
		return nil, err
	}
	if err := s.store.Put(s.key(req.GetKey()), req.GetValue()); err != nil {
		return nil, status.Errorf(codes.Internal, "put secret: %v", err)
	}
	return &pluginv1.PutSecretResponse{}, nil
}

func (s *secretServer) GetSecret(_ context.Context, req *pluginv1.GetSecretRequest) (*pluginv1.GetSecretResponse, error) {
	if err := s.check(req.GetKey()); err != nil {
		return nil, err
	}
	v, found, err := s.store.Get(s.key(req.GetKey()))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get secret: %v", err)
	}
	return &pluginv1.GetSecretResponse{Found: found, Value: v}, nil
}

func (s *secretServer) DeleteSecret(_ context.Context, req *pluginv1.DeleteSecretRequest) (*pluginv1.DeleteSecretResponse, error) {
	if err := s.check(req.GetKey()); err != nil {
		return nil, err
	}
	if err := s.store.Delete(s.key(req.GetKey())); err != nil {
		return nil, status.Errorf(codes.Internal, "delete secret: %v", err)
	}
	return &pluginv1.DeleteSecretResponse{}, nil
}
