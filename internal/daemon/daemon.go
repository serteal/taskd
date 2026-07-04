// Package daemon assembles taskd: store, TaskService handler, HTTP
// listeners, and configured syncers. The server speaks Connect, gRPC, and
// gRPC-Web on one port via h2c, so Go clients, browsers, and curl all use
// the same address.
package daemon

import (
	"context"
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

	"github.com/serteal/taskd/internal/extension"
	"github.com/serteal/taskd/internal/server"
	"github.com/serteal/taskd/internal/store"
	"github.com/serteal/taskd/internal/webui"
)

type Options struct {
	// Dir is the data directory; "" resolves via Dir().
	Dir string
	// Listen overrides the config file's TCP address when non-empty.
	Listen string
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

	st, err := store.Open(ctx, filepath.Join(dir, "tasks.db"), nil)
	if err != nil {
		return err
	}
	defer st.Close()

	exts, err := extension.Scan(filepath.Join(dir, "extensions"))
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	path, handler := server.New(st).Handler()
	mux.Handle(path, handler)
	// Health is transport-level, not part of the frozen proto API.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	// Extension web bundles + index, for the frontend's loader.
	mux.Handle("/ext/", extension.Handler(exts))
	// The web UI (or a pointer to how to build it); longer API patterns win.
	mux.Handle("/", webui.Handler())

	httpSrv := &http.Server{Handler: h2c.NewHandler(mux, &http2.Server{})}
	serveErr := make(chan error, 2)

	tcpLn, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.Listen, err)
	}
	go func() { serveErr <- httpSrv.Serve(tcpLn) }()
	log.Printf("taskd: listening on http://%s (data: %s)", cfg.Listen, dir)

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
	// public API — supervised here purely for operational convenience.
	for _, ext := range exts {
		if len(ext.Syncer) > 0 {
			go extension.Supervise(ctx, ext, cfg.Listen)
			log.Printf("taskd: extension %s: supervising syncer", ext.Name)
		}
		if ext.Web {
			log.Printf("taskd: extension %s: serving web bundle at /ext/%s/", ext.Name, ext.Name)
		}
	}

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
