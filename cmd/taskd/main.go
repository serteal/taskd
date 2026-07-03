// taskd is the task tracker daemon. Start it directly, via `task daemon run`,
// or from launchd/systemd — nothing is forced.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"todoapp/internal/daemon"
	"todoapp/internal/server"
)

func main() {
	dir := flag.String("dir", "", "data directory (default $TASKD_DIR or ~/.local/share/taskd)")
	retention := flag.Duration("retention", 30*24*time.Hour, "event log retention")
	version := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *version {
		fmt.Println(server.Version)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := daemon.Run(ctx, daemon.Config{Dir: *dir, Retention: *retention, Log: log}); err != nil {
		log.Error("taskd exited", "err", err)
		os.Exit(1)
	}
}
