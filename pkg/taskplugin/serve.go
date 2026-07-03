package taskplugin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
)

const (
	// SocketEnv names the environment variable carrying the unix socket path
	// the plugin must serve on. The host sets it; plugin authors never do.
	SocketEnv = "TASKPLUGIN_SOCKET"
	// CoreHostSocketEnv names the environment variable carrying the unix
	// socket path of the core's per-instance services (see DialCoreHost).
	CoreHostSocketEnv = "TASKPLUGIN_COREHOST_SOCKET"
)

// gracefulStopTimeout bounds how long a signal-triggered GracefulStop waits
// for in-flight RPCs before the server is stopped hard.
const gracefulStopTimeout = 5 * time.Second

// Serve runs the plugin process: it listens on the unix socket named by
// TASKPLUGIN_SOCKET and serves PluginService and ConnectorService there
// until SIGTERM/SIGINT (graceful stop, 5s hard-stop fallback) or until the
// listener closes underneath it. The socket path is used as-is — the host
// created its parent directory and owns cleanup of stale files.
//
// Configure is accepted exactly once per process (the host's contract); a
// second call fails with FailedPrecondition, as does any connector RPC
// before Configure. Resolve and HandleIntent are served only when c also
// implements Resolver / IntentHandler; WatchRemote is always Unimplemented
// for now (SNAPSHOT enumeration only).
func Serve(manifest *pluginv1.Manifest, c Connector) error {
	path := os.Getenv(SocketEnv)
	if path == "" {
		return fmt.Errorf("taskplugin: %s is not set; plugins are launched by the task host, which provides it", SocketEnv)
	}
	lis, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("taskplugin: listen on %s: %w", path, err)
	}

	state := &serveState{}
	srv := grpc.NewServer()
	pluginv1.RegisterPluginServiceServer(srv, &pluginServer{manifest: manifest, connector: c, state: state})
	pluginv1.RegisterConnectorServiceServer(srv, &connectorServer{connector: c, state: state})

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(lis) }()

	select {
	case err := <-serveErr:
		// The listener closed underneath us (host tore the socket down) or
		// accept failed fatally. The former is a normal way to go.
		if err == nil || errors.Is(err, grpc.ErrServerStopped) || errors.Is(err, net.ErrClosed) {
			return nil
		}
		return fmt.Errorf("taskplugin: serve: %w", err)
	case <-sigCh:
		stopped := make(chan struct{})
		go func() {
			srv.GracefulStop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-time.After(gracefulStopTimeout):
			srv.Stop()
			<-stopped
		}
		<-serveErr
		return nil
	}
}

// serveState is the per-process instance binding, shared by both services.
type serveState struct {
	mu         sync.Mutex
	configured bool
	instance   string
}

func (s *serveState) isConfigured() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.configured
}

type pluginServer struct {
	pluginv1.UnimplementedPluginServiceServer
	manifest  *pluginv1.Manifest
	connector Connector
	state     *serveState
}

func (s *pluginServer) GetManifest(context.Context, *pluginv1.GetManifestRequest) (*pluginv1.GetManifestResponse, error) {
	return &pluginv1.GetManifestResponse{Manifest: s.manifest}, nil
}

func (s *pluginServer) Configure(ctx context.Context, req *pluginv1.ConfigureRequest) (*pluginv1.ConfigureResponse, error) {
	// The lock is held across the delegate call so a concurrent second
	// Configure waits and then fails cleanly instead of racing. A rejected
	// config (error return) does not consume the one shot.
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	if s.state.configured {
		return nil, status.Errorf(codes.FailedPrecondition, "already configured as instance %q; one process serves one instance", s.state.instance)
	}
	caps, err := s.connector.Configure(ctx, req.GetInstanceName(), req.GetConfig())
	if err != nil {
		return nil, err
	}
	s.state.configured = true
	s.state.instance = req.GetInstanceName()
	return &pluginv1.ConfigureResponse{Capabilities: caps}, nil
}

type connectorServer struct {
	pluginv1.UnimplementedConnectorServiceServer
	connector Connector
	state     *serveState
}

func (s *connectorServer) Snapshot(_ *pluginv1.SnapshotRequest, stream pluginv1.ConnectorService_SnapshotServer) error {
	if !s.state.isConfigured() {
		return status.Error(codes.FailedPrecondition, "Snapshot called before Configure")
	}
	return s.connector.Snapshot(stream.Context(), func(item *pluginv1.RemoteItem) error {
		return stream.Send(&pluginv1.SnapshotResponse{Item: item})
	})
}

func (s *connectorServer) Resolve(ctx context.Context, req *pluginv1.ResolveRequest) (*pluginv1.ResolveResponse, error) {
	r, ok := s.connector.(Resolver)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "connector does not implement Resolve")
	}
	if !s.state.isConfigured() {
		return nil, status.Error(codes.FailedPrecondition, "Resolve called before Configure")
	}
	item, err := r.Resolve(ctx, req.GetRef())
	if err != nil {
		return nil, err
	}
	return &pluginv1.ResolveResponse{Item: item}, nil
}

func (s *connectorServer) HandleIntent(ctx context.Context, req *pluginv1.HandleIntentRequest) (*pluginv1.HandleIntentResponse, error) {
	h, ok := s.connector.(IntentHandler)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "connector does not implement HandleIntent")
	}
	if !s.state.isConfigured() {
		return nil, status.Error(codes.FailedPrecondition, "HandleIntent called before Configure")
	}
	item, err := h.HandleIntent(ctx, req)
	if err != nil {
		return nil, err
	}
	return &pluginv1.HandleIntentResponse{Item: item}, nil
}

func (s *connectorServer) WatchRemote(*pluginv1.WatchRemoteRequest, pluginv1.ConnectorService_WatchRemoteServer) error {
	return status.Error(codes.Unimplemented, "WatchRemote is not supported yet (SNAPSHOT enumeration only)")
}
