// Package daemon assembles and runs taskd: store + engine + hub + gRPC over
// a unix socket with owner-only permissions. Localhost TCP is deliberately
// not offered: it would be reachable by every process of every user on the
// machine; the socket's file permissions are the security boundary.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/grpc"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/clock"
	"todoapp/internal/feed"
	"todoapp/internal/plugin"
	"todoapp/internal/query"
	"todoapp/internal/schema"
	"todoapp/internal/secret"
	"todoapp/internal/server"
	"todoapp/internal/store"
	taskssync "todoapp/internal/sync"
)

type Config struct {
	// Dir holds the database and socket. Empty means DefaultDir().
	Dir string
	// Retention bounds the event log; expired watch cursors resync.
	// Zero means 30 days.
	Retention time.Duration
	// Clock and IDSeed are injectable for tests; zero values mean production
	// behavior (system clock, crypto entropy).
	Clock  clock.Clock
	IDSeed int64
	Log    *slog.Logger
}

// DefaultDir is ~/.local/share/taskd, overridable with TASKD_DIR.
func DefaultDir() string {
	if v := os.Getenv("TASKD_DIR"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".taskd"
	}
	return filepath.Join(home, ".local", "share", "taskd")
}

func SocketPath(dir string) string { return filepath.Join(dir, "taskd.sock") }
func DBPath(dir string) string     { return filepath.Join(dir, "task.db") }
func PidPath(dir string) string    { return filepath.Join(dir, "taskd.pid") }

const trimInterval = 6 * time.Hour

// Run serves until ctx is canceled. It refuses to start when another daemon
// already listens on the socket.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Dir == "" {
		cfg.Dir = DefaultDir()
	}
	if cfg.Retention <= 0 {
		cfg.Retention = 30 * 24 * time.Hour
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.System()
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return fmt.Errorf("data dir: %w", err)
	}

	fileCfg, err := loadFileConfig(cfg.Dir)
	if err != nil {
		return err
	}
	if cfg.Retention <= 0 && fileCfg.Retention > 0 {
		cfg.Retention = fileCfg.Retention
	}

	eng, err := query.NewEngine(cfg.Clock.Now)
	if err != nil {
		return fmt.Errorf("query engine: %w", err)
	}
	st, err := store.Open(store.Options{
		Path:    DBPath(cfg.Dir),
		Diff:    feed.Diff,
		Extract: eng.Extract,
		Now:     cfg.Clock.Now,
	})
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	defer st.Close()

	// The schema registry restores persisted manifests before anything
	// queries: kinds outlive their plugins.
	registry := schema.NewRegistry(st, eng)
	if err := registry.Load(ctx); err != nil {
		return err
	}

	if err := seedDefaultViews(ctx, st); err != nil {
		return fmt.Errorf("seed views: %w", err)
	}

	sock := SocketPath(cfg.Dir)
	// macOS caps sun_path at 104 bytes; fail with a real explanation instead
	// of bind's opaque EINVAL.
	if len(sock) > 100 {
		return fmt.Errorf("socket path %q exceeds the unix socket path limit; use a shorter data dir (TASKD_DIR)", sock)
	}
	if conn, err := net.DialTimeout("unix", sock, 500*time.Millisecond); err == nil {
		conn.Close()
		return fmt.Errorf("daemon already running on %s", sock)
	}
	_ = os.Remove(sock) // stale socket from an unclean shutdown
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	if err := os.Chmod(sock, 0o600); err != nil {
		ln.Close()
		return fmt.Errorf("socket permissions: %w", err)
	}

	// Pidfile for `task daemon stop`; the socket dial above already guards
	// against double-starts, so this is informational plus signal target.
	pidfile := PidPath(cfg.Dir)
	if err := os.WriteFile(pidfile, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
		ln.Close()
		return fmt.Errorf("pidfile: %w", err)
	}
	defer os.Remove(pidfile)

	hub := feed.NewHub()
	ids := clock.NewIDGen(cfg.Clock, cfg.IDSeed)
	srv := server.New(st, hub, eng, cfg.Clock, ids, registry.Kinds)
	g := grpc.NewServer()
	srv.Register(g)

	// Connector instances: plugin host + sync engine. A failing instance
	// logs and is skipped — one bad plugin never takes the daemon down.
	if len(fileCfg.Instances) > 0 {
		secrets, err := secret.Open("", cfg.Dir)
		if err != nil {
			return fmt.Errorf("secret store: %w", err)
		}
		host := plugin.NewHost(plugin.HostOptions{
			RuntimeDir: filepath.Join(cfg.Dir, "run"),
			Secrets:    secrets,
			Log:        cfg.Log,
			Clock:      cfg.Clock,
		})
		syncEng := taskssync.NewEngine(st, hub, cfg.Clock, ids, cfg.Log, 0)
		for _, ic := range fileCfg.Instances {
			inst, err := resolveInstance(cfg.Dir, ic)
			if err != nil {
				cfg.Log.Error("skipping instance", "err", err)
				continue
			}
			go func() {
				running, err := host.Start(ctx, inst, func(m *pluginv1.Manifest) error {
					return registry.RegisterManifest(ctx, m)
				})
				if err != nil {
					if ctx.Err() == nil {
						cfg.Log.Error("instance failed to start", "instance", inst.Name, "err", err)
					}
					return
				}
				defer running.Stop()
				cfg.Log.Info("instance running", "instance", inst.Name,
					"plugin", running.Manifest().GetName(), "poll", running.Poll())
				syncEng.RunInstance(ctx, running.Source(), running.Poll())
			}()
		}
	}

	// Retention: trim on start and periodically. Trimming never loses state
	// (the log is derivable history); lagging watchers resync.
	go func() {
		t := time.NewTicker(trimInterval)
		defer t.Stop()
		for {
			cutoff := cfg.Clock.Now().Add(-cfg.Retention)
			if n, err := st.TrimEvents(ctx, cutoff); err != nil {
				if ctx.Err() == nil {
					cfg.Log.Warn("event trim failed", "err", err)
				}
			} else if n > 0 {
				cfg.Log.Info("trimmed events", "count", n)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	go func() {
		<-ctx.Done()
		g.GracefulStop()
	}()

	cfg.Log.Info("taskd serving", "socket", sock, "version", server.Version)
	err = g.Serve(ln)
	_ = os.Remove(sock)
	if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return err
	}
	return nil
}

// seedDefaultViews creates the well-known views on first run only — they are
// conventions, not schema, and the user may redefine or delete them.
func seedDefaultViews(ctx context.Context, st store.Store) error {
	defaults := []*taskcorev1.View{
		{
			Name:        "inbox",
			Description: "Tracked items you have not triaged into todos yet",
			Filter:      `!has(item.todo)`,
			OrderBy:     "updated_at desc",
		},
		{
			Name:        "today",
			Description: "Due within 24h, or snoozed and now back",
			Filter:      `!completed && ((has_due && due < now + duration("24h")) || (snoozed_until > timestamp("1970-01-01T00:00:01Z") && !snoozed))`,
			OrderBy:     "due",
		},
		{
			Name:        "completed",
			Description: "The archive: everything done, links intact",
			Filter:      `completed`,
			OrderBy:     "updated_at desc",
		},
	}
	for _, v := range defaults {
		if _, err := st.GetView(ctx, v.GetName()); err == nil {
			continue
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err := st.SaveView(ctx, v); err != nil {
			return err
		}
	}
	return nil
}
