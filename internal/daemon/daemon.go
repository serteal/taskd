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

	"todoapp/internal/server"
	"todoapp/internal/store"
	"todoapp/internal/syncer"
	"todoapp/internal/webui"
	"todoapp/pkg/client"
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

	mux := http.NewServeMux()
	path, handler := server.New(st).Handler()
	mux.Handle(path, handler)
	// Health is transport-level, not part of the frozen proto API.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
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

	// Syncers connect back through the public API like any other client.
	tc := client.New("http://" + cfg.Listen)
	for _, y := range cfg.Syncers {
		scfg, err := y.toConfig()
		if err != nil {
			return err
		}
		s, err := syncer.New(scfg)
		if err != nil {
			return err
		}
		go syncer.Run(ctx, s, scfg.Interval, tc)
		log.Printf("taskd: syncer %s every %s", s.Source(), effectiveInterval(scfg.Interval))
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

func effectiveInterval(d time.Duration) time.Duration {
	if d <= 0 {
		return syncer.DefaultInterval
	}
	return d
}
