// Package plugin is the connector plugin host (DESIGN.md §10, §14): one
// supervised child process per configured instance, gRPC over per-instance
// unix sockets. The plugin serves PluginService/ConnectorService on p.sock;
// the host serves the least-privilege corehost surface (SecretService only)
// back to the plugin on ch.sock — plugins never see the client API, and each
// instance's secrets are namespaced so one instance can never read
// another's.
package plugin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
	"todoapp/internal/clock"
	"todoapp/internal/secret"
)

// Instance is one configured connector instance ("github@work" and
// "github@oss" are two instances of one plugin binary).
type Instance struct {
	Name   string         // "cal@personal" — the link namespace
	Binary string         // path to the plugin executable
	Config map[string]any // from the user's config file
	Poll   time.Duration  // 0 → use capability hint → floor 30s, default 5m
}

// HostOptions configures a Host. Zero values take the documented defaults.
type HostOptions struct {
	RuntimeDir string       // sockets live under <RuntimeDir>/<slug>/ — keep it short (unix socket path cap)
	Secrets    secret.Store // backing store for the per-instance SecretService
	Log        *slog.Logger // plugin stdout/stderr and supervision events
	Clock      clock.Clock  // time source for uptime accounting

	// Schedule constants, injectable so tests can run fast.
	DialTimeout    time.Duration // handshake cap after spawn; default 5s
	StopGrace      time.Duration // SIGTERM → SIGKILL grace; default 3s
	BackoffInitial time.Duration // first restart delay; default 1s
	BackoffCap     time.Duration // restart delay ceiling; default 60s
	BackoffReset   time.Duration // uptime that resets the backoff; default 5m
}

// Host spawns and supervises plugin instances.
type Host struct {
	opts HostOptions

	mu sync.Mutex
	n  int // next instance slug index
}

// NewHost returns a Host with defaults applied for any zero option.
func NewHost(opts HostOptions) *Host {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.Clock == nil {
		opts.Clock = clock.System()
	}
	if opts.DialTimeout <= 0 {
		opts.DialTimeout = 5 * time.Second
	}
	if opts.StopGrace <= 0 {
		opts.StopGrace = 3 * time.Second
	}
	if opts.BackoffInitial <= 0 {
		opts.BackoffInitial = time.Second
	}
	if opts.BackoffCap <= 0 {
		opts.BackoffCap = 60 * time.Second
	}
	if opts.BackoffReset <= 0 {
		opts.BackoffReset = 5 * time.Minute
	}
	return &Host{opts: opts}
}

// Start spawns, handshakes, and supervises one instance until ctx ends.
// The registerManifest callback runs after GetManifest and before Configure;
// an error aborts the start (the schema registry uses this to refuse
// breaking manifest changes). On restarts the callback runs again with the
// re-fetched manifest.
func (h *Host) Start(ctx context.Context, inst Instance, registerManifest func(*pluginv1.Manifest) error) (*Running, error) {
	if inst.Name == "" {
		return nil, errors.New("plugin: instance name required")
	}
	if inst.Binary == "" {
		return nil, fmt.Errorf("plugin %s: binary path required", inst.Name)
	}
	if registerManifest == nil {
		registerManifest = func(*pluginv1.Manifest) error { return nil }
	}
	cfg, err := structpb.NewStruct(inst.Config)
	if err != nil {
		return nil, fmt.Errorf("plugin %s: config not representable: %w", inst.Name, err)
	}

	// Short per-instance slug keeps socket paths under the unix cap.
	h.mu.Lock()
	slug := fmt.Sprintf("i%d", h.n)
	h.n++
	h.mu.Unlock()

	dir := filepath.Join(h.opts.RuntimeDir, slug)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("plugin %s: runtime dir: %w", inst.Name, err)
	}

	// Corehost socket first: the plugin may call SecretService during
	// Configure. This socket is the plugin's ONLY core surface.
	chSock := filepath.Join(dir, "ch.sock")
	_ = os.Remove(chSock)
	lis, err := net.Listen("unix", chSock)
	if err != nil {
		return nil, fmt.Errorf("plugin %s: corehost listen: %w", inst.Name, err)
	}
	_ = os.Chmod(chSock, 0o600)
	coreSrv := grpc.NewServer()
	pluginv1.RegisterSecretServiceServer(coreSrv, &secretServer{ns: inst.Name, store: h.opts.Secrets})
	go func() { _ = coreSrv.Serve(lis) }()

	sctx, cancel := context.WithCancel(ctx)
	r := &Running{
		host:             h,
		inst:             inst,
		cfg:              cfg,
		pSock:            filepath.Join(dir, "p.sock"),
		chSock:           chSock,
		registerManifest: registerManifest,
		log:              h.opts.Log.With("instance", inst.Name),
		cancel:           cancel,
		done:             make(chan struct{}),
		coreSrv:          coreSrv,
	}
	if err := r.launch(sctx); err != nil {
		cancel()
		coreSrv.Stop()
		return nil, fmt.Errorf("plugin %s: %w", inst.Name, err)
	}
	go r.supervise(sctx)
	return r, nil
}
