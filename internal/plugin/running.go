package plugin

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
)

const (
	pollFloor   = 30 * time.Second
	pollDefault = 5 * time.Minute
)

// Running is one live (supervised) plugin instance. It outlives individual
// plugin processes: crashes are restarted with backoff, and the Source keeps
// working across restarts (returning errors while the plugin is down).
type Running struct {
	host             *Host
	inst             Instance
	cfg              *structpb.Struct
	pSock, chSock    string
	registerManifest func(*pluginv1.Manifest) error
	log              *slog.Logger

	cancel  context.CancelFunc
	done    chan struct{} // closed when supervision has fully shut down
	coreSrv *grpc.Server

	mu        sync.Mutex
	manifest  *pluginv1.Manifest
	caps      *pluginv1.Capabilities
	conn      *grpc.ClientConn
	connector pluginv1.ConnectorServiceClient // nil while the plugin is down
	cmd       *exec.Cmd
	startedAt time.Time
}

// Manifest returns the most recently registered manifest.
func (r *Running) Manifest() *pluginv1.Manifest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.manifest
}

// Capabilities returns the capabilities from the most recent Configure.
func (r *Running) Capabilities() *pluginv1.Capabilities {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.caps
}

// Poll returns the resolved poll interval: Instance.Poll, else the
// capability hint, floored at 30s, defaulting to 5m.
func (r *Running) Poll() time.Duration {
	r.mu.Lock()
	caps := r.caps
	r.mu.Unlock()
	return resolvePoll(r.inst.Poll, caps.GetPollInterval())
}

func resolvePoll(configured time.Duration, hint *durationpb.Duration) time.Duration {
	d := configured
	if d == 0 && hint != nil {
		d = hint.AsDuration()
	}
	if d == 0 {
		return pollDefault
	}
	if d < pollFloor {
		return pollFloor
	}
	return d
}

// Stop terminates the instance: SIGTERM, then SIGKILL after the grace
// period. Idempotent; returns once the process has exited and supervision
// has shut down.
func (r *Running) Stop() {
	r.cancel()
	<-r.done
}

// launch spawns one plugin process and completes the handshake:
// GetManifest → registerManifest → Configure. Any failure kills the process
// and returns the error. On success the connector client is swapped in.
func (r *Running) launch(ctx context.Context) error {
	_ = os.Remove(r.pSock)
	cmd := exec.Command(r.inst.Binary)
	cmd.Env = append(os.Environ(),
		"TASKPLUGIN_SOCKET="+r.pSock,
		"TASKPLUGIN_COREHOST_SOCKET="+r.chSock,
	)
	cmd.Stdout = &lineWriter{emit: func(line string) { r.log.Info(line, "stream", "stdout") }}
	cmd.Stderr = &lineWriter{emit: func(line string) { r.log.Warn(line, "stream", "stderr") }}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn %s: %w", r.inst.Binary, err)
	}

	// Aggressive connect backoff: the socket appears whenever the plugin
	// gets around to serving; WaitForReady + DialTimeout caps the wait.
	conn, err := grpc.NewClient("unix://"+r.pSock,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff: backoff.Config{
				BaseDelay:  20 * time.Millisecond,
				Multiplier: 1.5,
				Jitter:     0.2,
				MaxDelay:   250 * time.Millisecond,
			},
			MinConnectTimeout: time.Second,
		}),
	)
	if err != nil {
		killWait(cmd)
		return fmt.Errorf("grpc client: %w", err)
	}
	fail := func(err error) error {
		_ = conn.Close()
		killWait(cmd)
		return err
	}

	hctx, hcancel := context.WithTimeout(ctx, r.host.opts.DialTimeout)
	defer hcancel()
	pc := pluginv1.NewPluginServiceClient(conn)
	mresp, err := pc.GetManifest(hctx, &pluginv1.GetManifestRequest{}, grpc.WaitForReady(true))
	if err != nil {
		return fail(fmt.Errorf("get manifest: %w", err))
	}
	m := mresp.GetManifest()

	r.mu.Lock()
	prev := r.manifest
	r.mu.Unlock()
	if prev != nil && m.GetName() != prev.GetName() {
		return fail(fmt.Errorf("manifest name changed across restart: %q → %q", prev.GetName(), m.GetName()))
	}
	if err := r.registerManifest(m); err != nil {
		return fail(fmt.Errorf("register manifest: %w", err))
	}

	cctx, ccancel := context.WithTimeout(ctx, r.host.opts.DialTimeout)
	defer ccancel()
	cresp, err := pc.Configure(cctx, &pluginv1.ConfigureRequest{
		InstanceName: r.inst.Name,
		Config:       r.cfg,
	})
	if err != nil {
		return fail(fmt.Errorf("configure: %w", err))
	}

	r.mu.Lock()
	r.manifest = m
	r.caps = cresp.GetCapabilities()
	r.conn = conn
	r.connector = pluginv1.NewConnectorServiceClient(conn)
	r.cmd = cmd
	r.startedAt = r.host.opts.Clock.Now()
	r.mu.Unlock()
	return nil
}

// supervise watches the current process and restarts it with exponential
// backoff until ctx ends; then it terminates the process and shuts the
// corehost surface down.
func (r *Running) supervise(ctx context.Context) {
	defer close(r.done)
	defer r.coreSrv.Stop()

	opts := r.host.opts
	delay := opts.BackoffInitial
	for {
		r.mu.Lock()
		cmd := r.cmd
		started := r.startedAt
		r.mu.Unlock()

		exited := make(chan error, 1)
		go func() { exited <- cmd.Wait() }()

		select {
		case <-ctx.Done():
			r.terminate(cmd, exited)
			r.setDown()
			return
		case werr := <-exited:
			r.setDown()
			if ctx.Err() != nil {
				return
			}
			if opts.Clock.Now().Sub(started) >= opts.BackoffReset {
				delay = opts.BackoffInitial
			}
			r.log.Warn("plugin exited; restarting", "err", werr, "backoff", delay)
		}

		// Restart loop: sleep the backoff, try a full relaunch, repeat on
		// failure with a doubled (capped) delay.
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			if delay *= 2; delay > opts.BackoffCap {
				delay = opts.BackoffCap
			}
			if err := r.launch(ctx); err != nil {
				if ctx.Err() != nil {
					return
				}
				r.log.Warn("plugin restart failed", "err", err, "backoff", delay)
				continue
			}
			r.log.Info("plugin restarted")
			break
		}
	}
}

// terminate implements the SIGTERM → grace → SIGKILL shutdown of one
// process; exited must carry its Wait result.
func (r *Running) terminate(cmd *exec.Cmd, exited <-chan error) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-exited:
	case <-time.After(r.host.opts.StopGrace):
		_ = cmd.Process.Kill()
		<-exited
	}
}

// setDown drops the connector client so Source calls fail fast while the
// plugin is down.
func (r *Running) setDown() {
	r.mu.Lock()
	if r.conn != nil {
		_ = r.conn.Close()
	}
	r.conn = nil
	r.connector = nil
	r.mu.Unlock()
}

// killWait force-kills a half-started process and reaps it.
func killWait(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait()
}

// lineWriter turns a child's stdout/stderr byte stream into per-line log
// records. Each stream gets its own writer, written from the single copier
// goroutine os/exec runs per stream.
type lineWriter struct {
	emit func(string)
	buf  []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		line := string(w.buf[:i])
		if n := len(line); n > 0 && line[n-1] == '\r' {
			line = line[:n-1]
		}
		if line != "" {
			w.emit(line)
		}
		w.buf = w.buf[i+1:]
	}
}
