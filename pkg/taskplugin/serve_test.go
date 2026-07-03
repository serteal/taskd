package taskplugin_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
	"todoapp/pkg/taskplugin"
)

// testConn is a minimal Connector: no Resolver, no IntentHandler.
type testConn struct {
	mu       sync.Mutex
	instance string
	cfgErr   error
	items    []*pluginv1.RemoteItem
}

func (c *testConn) Configure(_ context.Context, instance string, _ *structpb.Struct) (*pluginv1.Capabilities, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cfgErr != nil {
		err := c.cfgErr
		c.cfgErr = nil
		return nil, err
	}
	c.instance = instance
	return &pluginv1.Capabilities{Enumeration: pluginv1.Enumeration_ENUMERATION_SNAPSHOT}, nil
}

func (c *testConn) Snapshot(_ context.Context, emit func(*pluginv1.RemoteItem) error) error {
	c.mu.Lock()
	items := c.items
	c.mu.Unlock()
	for _, it := range items {
		if err := emit(it); err != nil {
			return err
		}
	}
	return nil
}

func (c *testConn) gotInstance() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.instance
}

// srvHandle wraps a Serve goroutine started against a temp socket. stop
// sends SIGTERM to the test process (Serve subscribes via signal.Notify;
// tests are sequential, so exactly one Serve is listening at a time) and
// waits for Serve to return.
type srvHandle struct {
	conn *grpc.ClientConn
	done chan error
	once sync.Once
	err  error
}

func (s *srvHandle) stop(t *testing.T) error {
	t.Helper()
	s.once.Do(func() {
		if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
			t.Fatalf("kill: %v", err)
		}
		select {
		case s.err = <-s.done:
		case <-time.After(10 * time.Second):
			t.Fatal("Serve did not exit after SIGTERM")
		}
	})
	return s.err
}

func startServe(t *testing.T, manifest *pluginv1.Manifest, c taskplugin.Connector) *srvHandle {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "s")
	t.Setenv(taskplugin.SocketEnv, sock)

	h := &srvHandle{done: make(chan error, 1)}
	go func() { h.done <- taskplugin.Serve(manifest, c) }()
	t.Cleanup(func() {
		if err := h.stop(t); err != nil {
			t.Errorf("Serve returned error: %v", err)
		}
	})

	conn, err := grpc.NewClient("unix://"+sock,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	h.conn = conn

	// Wait for the socket to be served.
	pc := pluginv1.NewPluginServiceClient(conn)
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := pc.GetManifest(context.Background(), &pluginv1.GetManifestRequest{})
		if err == nil {
			return h
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never came up: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func configure(t *testing.T, conn *grpc.ClientConn, instance string) *pluginv1.ConfigureResponse {
	t.Helper()
	resp, err := pluginv1.NewPluginServiceClient(conn).Configure(context.Background(),
		&pluginv1.ConfigureRequest{InstanceName: instance})
	if err != nil {
		t.Fatalf("Configure: %v", err)
	}
	return resp
}

func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if status.Code(err) != want {
		t.Fatalf("got error %v (code %v), want code %v", err, status.Code(err), want)
	}
}

func TestManifest(t *testing.T) {
	m := &pluginv1.Manifest{
		Name:     "fake",
		Version:  "1.2.3",
		Services: []string{"connector"},
		Kinds:    []*pluginv1.KindRegistration{{Kind: "fake.item"}},
	}
	h := startServe(t, m, &testConn{})

	resp, err := pluginv1.NewPluginServiceClient(h.conn).GetManifest(context.Background(), &pluginv1.GetManifestRequest{})
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	got := resp.GetManifest()
	if got.GetName() != "fake" || got.GetVersion() != "1.2.3" {
		t.Errorf("manifest = %v/%v, want fake/1.2.3", got.GetName(), got.GetVersion())
	}
	if len(got.GetKinds()) != 1 || got.GetKinds()[0].GetKind() != "fake.item" {
		t.Errorf("kinds = %v, want [fake.item]", got.GetKinds())
	}
	if len(got.GetServices()) != 1 || got.GetServices()[0] != "connector" {
		t.Errorf("services = %v, want [connector]", got.GetServices())
	}
}

func TestCfgOnce(t *testing.T) {
	c := &testConn{}
	h := startServe(t, &pluginv1.Manifest{Name: "fake"}, c)
	pc := pluginv1.NewPluginServiceClient(h.conn)

	resp := configure(t, h.conn, "fake@a")
	if got := resp.GetCapabilities().GetEnumeration(); got != pluginv1.Enumeration_ENUMERATION_SNAPSHOT {
		t.Errorf("enumeration = %v, want SNAPSHOT", got)
	}
	if got := c.gotInstance(); got != "fake@a" {
		t.Errorf("connector saw instance %q, want %q", got, "fake@a")
	}

	_, err := pc.Configure(context.Background(), &pluginv1.ConfigureRequest{InstanceName: "fake@b"})
	wantCode(t, err, codes.FailedPrecondition)
}

func TestCfgRetry(t *testing.T) {
	// A rejected config must not consume the one accepted Configure.
	c := &testConn{cfgErr: status.Error(codes.InvalidArgument, "bad config")}
	h := startServe(t, &pluginv1.Manifest{Name: "fake"}, c)
	pc := pluginv1.NewPluginServiceClient(h.conn)

	_, err := pc.Configure(context.Background(), &pluginv1.ConfigureRequest{InstanceName: "fake@a"})
	wantCode(t, err, codes.InvalidArgument)
	configure(t, h.conn, "fake@a") // second attempt succeeds
}

func TestSnap(t *testing.T) {
	c := &testConn{items: []*pluginv1.RemoteItem{
		{ExternalId: "a", Kind: "fake.item", Title: "A"},
		{ExternalId: "b", Kind: "fake.item", Title: "B"},
		{ExternalId: "c", Kind: "fake.item", Title: "C"},
	}}
	h := startServe(t, &pluginv1.Manifest{Name: "fake"}, c)
	cc := pluginv1.NewConnectorServiceClient(h.conn)

	// Before Configure: FailedPrecondition.
	stream, err := cc.Snapshot(context.Background(), &pluginv1.SnapshotRequest{})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	_, err = stream.Recv()
	wantCode(t, err, codes.FailedPrecondition)

	configure(t, h.conn, "fake@a")

	stream, err = cc.Snapshot(context.Background(), &pluginv1.SnapshotRequest{})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	var got []string
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		got = append(got, resp.GetItem().GetExternalId())
	}
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("streamed %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("streamed %v, want %v (order must be preserved)", got, want)
		}
	}
}

func TestUnimpl(t *testing.T) {
	h := startServe(t, &pluginv1.Manifest{Name: "fake"}, &testConn{})
	configure(t, h.conn, "fake@a")
	cc := pluginv1.NewConnectorServiceClient(h.conn)

	_, err := cc.Resolve(context.Background(), &pluginv1.ResolveRequest{Ref: "https://x"})
	wantCode(t, err, codes.Unimplemented)

	_, err = cc.HandleIntent(context.Background(), &pluginv1.HandleIntentRequest{ExternalId: "a"})
	wantCode(t, err, codes.Unimplemented)

	stream, err := cc.WatchRemote(context.Background(), &pluginv1.WatchRemoteRequest{})
	if err != nil {
		t.Fatalf("WatchRemote: %v", err)
	}
	_, err = stream.Recv()
	wantCode(t, err, codes.Unimplemented)
}

func TestStop(t *testing.T) {
	h := startServe(t, &pluginv1.Manifest{Name: "fake"}, &testConn{})

	if err := h.stop(t); err != nil {
		t.Fatalf("Serve after SIGTERM = %v, want nil", err)
	}
	// The socket is down: a fresh RPC must fail.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := pluginv1.NewPluginServiceClient(h.conn).GetManifest(ctx, &pluginv1.GetManifestRequest{})
	if err == nil {
		t.Fatal("GetManifest succeeded after shutdown, want error")
	}
}

func TestNoEnv(t *testing.T) {
	t.Setenv(taskplugin.SocketEnv, "")
	err := taskplugin.Serve(&pluginv1.Manifest{Name: "fake"}, &testConn{})
	if err == nil {
		t.Fatal("Serve without TASKPLUGIN_SOCKET succeeded, want error")
	}
}
