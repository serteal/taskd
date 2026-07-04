// Command taskd is the task daemon: it owns the SQLite store and serves the
// TaskService API.
package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"todoapp/internal/daemon"
)

func main() {
	dir := flag.String("dir", "", "data directory (default $TASKD_DIR or ~/.taskd)")
	listen := flag.String("listen", "", "TCP listen address (default from config.yaml or 127.0.0.1:7517)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := daemon.Run(ctx, daemon.Options{Dir: *dir, Listen: *listen}); err != nil {
		log.Fatalf("taskd: %v", err)
	}
}
