// Command taskd is the task daemon: it owns the SQLite store and serves the
// TaskService API.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os/signal"
	"syscall"

	"github.com/serteal/taskd/internal/daemon"
	"github.com/serteal/taskd/internal/version"
)

func main() {
	dir := flag.String("dir", "", "data directory (default $TASKD_DIR or ~/.taskd)")
	listen := flag.String("listen", "", "TCP listen address (default from config.yaml or 127.0.0.1:8888)")
	open := flag.Bool("open", false, "open the web app in a browser on startup")
	noOpen := flag.Bool("no-open", false, "do not auto-open the web app on first run")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("taskd %s\n", version.Version)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := daemon.Run(ctx, daemon.Options{Dir: *dir, Listen: *listen, Open: *open, NoOpen: *noOpen}); err != nil {
		log.Fatalf("taskd: %v", err)
	}
}
