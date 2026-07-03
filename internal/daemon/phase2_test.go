package daemon

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

// TestPluginE2E is the phase-2 proof: a real plugin subprocess (the ics
// connector) configured via config.yaml, mirrored into the store by the
// sync engine, queryable through descriptor-registered extension types, and
// promotable without sync ever touching the todo layer.
func TestPluginE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a real plugin binary")
	}
	dir, err := os.MkdirTemp("", "p2") // short: unix socket path cap
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	// Build the real connector binary into the daemon's plugins dir.
	if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o700); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "plugins", "ics")
	build := exec.Command("go", "build", "-o", bin, "todoapp/plugins/ics/cmd")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("building ics plugin: %v", err)
	}

	// A live calendar file: one timed event tomorrow, one next week.
	cal := filepath.Join(dir, "cal.ics")
	writeCal := func(summary string) {
		lines := []string{
			"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//e2e//EN",
			"BEGIN:VEVENT", "UID:standup@e2e",
			"DTSTART:" + time.Now().UTC().Add(24*time.Hour).Format("20060102T150405Z"),
			"DTSTAMP:20260701T000000Z", "SUMMARY:" + summary, "END:VEVENT",
			"BEGIN:VEVENT", "UID:review@e2e",
			"DTSTART:" + time.Now().UTC().Add(7*24*time.Hour).Format("20060102T150405Z"),
			"DTSTAMP:20260701T000000Z", "SUMMARY:design review", "END:VEVENT",
			"END:VCALENDAR",
		}
		if err := os.WriteFile(cal, []byte(strings.Join(lines, "\r\n")+"\r\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeCal("standup")

	cfgYAML := fmt.Sprintf("instances:\n  - name: cal@e2e\n    plugin: ics\n    poll: 30s\n    config:\n      url: file://%s\n", cal)
	if err := os.WriteFile(ConfigPath(dir), []byte(cfgYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	go func() { errc <- Run(ctx, Config{Dir: dir, Log: log}) }()
	t.Cleanup(func() {
		cancel()
		if err := <-errc; err != nil {
			t.Errorf("daemon exit: %v", err)
		}
	})
	waitFor(t, 5*time.Second, func() bool {
		_, err := os.Stat(SocketPath(dir))
		return err == nil
	})

	conn, err := grpc.NewClient("unix://"+SocketPath(dir), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	rpcCtx := metadata.AppendToOutgoingContext(ctx, "x-task-client", "p2-test")
	items := taskcorev1.NewItemServiceClient(conn)

	// Mirrors appear via the first sync cycle.
	var mirrored []*taskcorev1.Item
	waitFor(t, 15*time.Second, func() bool {
		resp, err := items.QueryItems(rpcCtx, &taskcorev1.QueryItemsRequest{Filter: `kind == "calendar.event"`})
		if err != nil {
			return false
		}
		mirrored = resp.GetItems()
		return len(mirrored) == 2
	})

	// They are un-triaged: inbox by convention, absent from active defaults.
	resp, err := items.QueryItems(rpcCtx, &taskcorev1.QueryItemsRequest{Filter: `!has(item.todo)`})
	if err != nil || len(resp.GetItems()) != 2 {
		t.Fatalf("inbox = %d items (%v), want 2", len(resp.GetItems()), err)
	}

	// The virtual due resolves through the plugin's Any payload via
	// descriptors the daemon never linked — the phase's core promise.
	resp, err = items.QueryItems(rpcCtx, &taskcorev1.QueryItemsRequest{
		Filter: `kind == "calendar.event" && has_due && due < now + duration("48h")`,
	})
	if err != nil {
		t.Fatalf("virtual-due filter: %v", err)
	}
	if len(resp.GetItems()) != 1 || resp.GetItems()[0].GetMirror().GetTitle() != "standup" {
		t.Fatalf("virtual-due filter matched %d, want just the standup", len(resp.GetItems()))
	}

	// Promote one into a todo; sync must never touch the user layer.
	standupID := resp.GetItems()[0].GetId()
	if _, err := items.UpdateItem(rpcCtx, &taskcorev1.UpdateItemRequest{
		Id:         standupID,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"todo.labels", "todo.project"}},
		Item: &taskcorev1.Item{Todo: &taskcorev1.Todo{
			Labels: []string{"meeting"}, Project: "work",
		}},
	}); err != nil {
		t.Fatalf("promoting a mirror: %v", err)
	}

	// A remote edit propagates on the next cycle — and the todo survives.
	writeCal("standup (moved!)")
	waitFor(t, 45*time.Second, func() bool {
		got, err := items.GetItem(rpcCtx, &taskcorev1.GetItemRequest{Id: standupID})
		return err == nil && got.GetItem().GetMirror().GetTitle() == "standup (moved!)"
	})
	got, err := items.GetItem(rpcCtx, &taskcorev1.GetItemRequest{Id: standupID})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetItem().GetTodo().GetProject() != "work" || len(got.GetItem().GetTodo().GetLabels()) != 1 {
		t.Fatalf("sync damaged the todo layer: %v", got.GetItem().GetTodo())
	}

	// The registry serves the plugin's kinds alongside the native one.
	schema := taskcorev1.NewSchemaServiceClient(conn)
	kinds, err := schema.ListKinds(rpcCtx, &taskcorev1.ListKindsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, k := range kinds.GetKinds() {
		names[k.GetKind()] = true
	}
	for _, want := range []string{"task", "calendar.event", "calendar.series"} {
		if !names[want] {
			t.Fatalf("ListKinds missing %q: %v", want, names)
		}
	}
}
