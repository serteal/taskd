// Package syncer is the Go SDK for writing taskd integrations. A syncer is
// an ordinary API client — it has no privileged access — that reconciles an
// external system into tasks through TaskService.UpsertExternalTasks, and
// optionally reacts to task changes (write-back) through WatchTasks.
//
// Syncers run as standalone binaries. When the daemon supervises one (an
// extension's manifest.json declares the command), TASKD_ADDR is injected
// and the working directory is the extension's own folder, so relative
// config paths just work. The same binary runs fine under launchd/systemd
// or by hand.
package syncer

import (
	"context"
	"errors"
	"log"
	"time"

	"connectrpc.com/connect"

	taskpb "github.com/serteal/taskd/gen/task"
	"github.com/serteal/taskd/gen/task/taskconnect"
	"github.com/serteal/taskd/pkg/client"
)

// Client dials the daemon from TASKD_ADDR (injected by the daemon's
// extension host) or the default local address.
func Client() taskconnect.TaskServiceClient {
	return client.New(client.Target())
}

// Run calls sync immediately and then on every interval tick until ctx
// ends. Sync errors are logged and the loop continues: the external system
// being down must not take the syncer down.
func Run(ctx context.Context, name string, interval time.Duration, sync func(context.Context) error) {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	once := func() {
		if err := sync(ctx); err != nil && ctx.Err() == nil {
			log.Printf("%s: sync: %v", name, err)
		}
	}
	once()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			once()
		}
	}
}

// WatchChanges streams task creations and updates to fn until ctx ends —
// the write-back primitive (e.g. "task completed locally → close the remote
// issue"). Deletions and the protocol handshake are skipped. The stream is
// re-opened with backoff on any error; fn errors are logged, not fatal.
// Note the watch has no history: changes during a disconnect are missed, so
// pair write-back decisions with state checks in the regular sync pass.
func WatchChanges(ctx context.Context, tc taskconnect.TaskServiceClient, name string, fn func(*taskpb.Task) error) {
	backoff := time.Second
	for ctx.Err() == nil {
		stream, err := tc.WatchTasks(ctx, connect.NewRequest(&taskpb.WatchTasksRequest{}))
		if err == nil {
			backoff = time.Second
			for stream.Receive() {
				t := stream.Msg().GetTask()
				if t == nil {
					continue // handshake, deletion, or a newer change kind
				}
				if err := fn(t); err != nil {
					log.Printf("%s: watch handler: %v", name, err)
				}
			}
			err = stream.Err()
		}
		if ctx.Err() != nil {
			return
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("%s: watch: %v (reconnecting)", name, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}
