// Package daemon assembles taskd: store, TaskService handler, HTTP
// listeners, and configured syncers. The server speaks Connect, gRPC, and
// gRPC-Web on one port via h2c, so Go clients, browsers, and curl all use
// the same address.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/serteal/taskd/internal/adminserver"
	"github.com/serteal/taskd/internal/extension"
	"github.com/serteal/taskd/internal/server"
	"github.com/serteal/taskd/internal/store"
	"github.com/serteal/taskd/internal/version"
	"github.com/serteal/taskd/internal/webui"
)

type Options struct {
	// Dir is the data directory; "" resolves via Dir().
	Dir string
	// Listen overrides the config file's TCP address when non-empty.
	Listen string
	// Open forces opening the web app in a browser on startup; NoOpen
	// suppresses the first-run auto-open. Flags override the config's open:.
	Open   bool
	NoOpen bool
}

// Run serves until ctx is canceled, then shuts down gracefully.
func Run(ctx context.Context, opts Options) error {
	dir := opts.Dir
	if dir == "" {
		var err error
		if dir, err = Dir(); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	cfg, err := loadFileConfig(dir)
	if err != nil {
		return err
	}
	if opts.Listen != "" {
		cfg.Listen = opts.Listen
	}

	// Detect a first run before opening the store, since Open creates the file.
	first := firstRun(dir)

	st, err := store.Open(ctx, filepath.Join(dir, "tasks.db"), nil)
	if err != nil {
		return err
	}
	defer st.Close()

	exts, err := extension.Scan(filepath.Join(dir, "extensions"))
	if err != nil {
		return err
	}
	disabled := make(map[string]bool, len(cfg.Extensions.Disabled))
	for _, name := range cfg.Extensions.Disabled {
		disabled[name] = true
	}
	host := extension.NewHost(exts, cfg.Listen, disabled)

	mux := http.NewServeMux()
	path, handler := server.New(st).Handler()
	mux.Handle(path, handler)
	// Admin API: enabling/disabling extensions (and, later, daemon settings).
	// A separate service from the frozen task.TaskService by design.
	adminPath, adminHandler := adminserver.New(host, func(names []string) error {
		return saveFileConfig(dir, FileConfig{Extensions: ExtensionsConfig{Disabled: names}})
	}).Handler()
	mux.Handle(adminPath, adminHandler)
	// Health is transport-level, not part of the frozen proto API.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	// Build identity, also transport-level (separate from the frozen proto API).
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "taskd", "version": version.Version})
	})
	// Extension web bundles + index, for the frontend's loader.
	mux.Handle("/ext/", host.Handler())
	// The web UI (or a pointer to how to build it); longer API patterns win.
	mux.Handle("/", webui.Handler())

	httpSrv := &http.Server{Handler: h2c.NewHandler(mux, &http2.Server{})}
	serveErr := make(chan error, 2)

	tcpLn, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.Listen, err)
	}
	go func() { serveErr <- httpSrv.Serve(tcpLn) }()
	webURL := "http://" + cfg.Listen
	log.Printf("taskd: listening on %s (data: %s)", webURL, dir)

	// Open the web app when this run asks for it, best-effort: a browser that
	// fails to launch only logs. Skipped without a TCP address (nothing to
	// point a browser at over a unix socket). TASKD_NO_OPEN suppresses even an
	// explicit -open: harnesses that spawn many first-run daemons (the web e2e
	// suite starts one per test) must never open browser windows.
	if cfg.Listen != "" && os.Getenv("TASKD_NO_OPEN") == "" && wantOpen(first, opts.Open, opts.NoOpen, cfg.Open) {
		log.Printf("taskd: opening web app at %s", webURL)
		opener := openURL
		go func() {
			if err := opener(webURL); err != nil {
				log.Printf("taskd: could not open browser: %v", err)
			}
		}()
	}

	if cfg.Socket != "" {
		_ = os.Remove(cfg.Socket) // stale socket from an unclean exit
		unixLn, err := net.Listen("unix", cfg.Socket)
		if err != nil {
			return fmt.Errorf("listen %s: %w", cfg.Socket, err)
		}
		if err := os.Chmod(cfg.Socket, 0o600); err != nil {
			return err
		}
		defer os.Remove(cfg.Socket)
		go func() { serveErr <- httpSrv.Serve(unixLn) }()
		log.Printf("taskd: listening on unix://%s", cfg.Socket)
	}

	// Extension syncers are separate processes talking back through the
	// public API — supervised here purely for operational convenience. The
	// host skips disabled extensions and can start/stop them at runtime when
	// the admin API toggles one.
	host.Start(ctx)

	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}
