package daemon

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"

	taskpb "todoapp/gen/task"
	"todoapp/pkg/client"
)

// TestDaemonEndToEnd boots the real daemon — config file, TCP + unix
// listeners, an ICS syncer against a fixture file — and drives it through
// the public API only.
func TestDaemonEndToEnd(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "taskd.sock")
	addr := freeAddr(t)

	// A calendar with one event tomorrow: the syncer should import it soon
	// after boot (interval only schedules re-syncs; the first sync is
	// immediate).
	ics := fmt.Sprintf("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//t//EN\r\nBEGIN:VEVENT\r\nUID:ev-1\r\nDTSTART:%s\r\nDTEND:%s\r\nSUMMARY:Dentist\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n",
		time.Now().Add(24*time.Hour).UTC().Format("20060102T150405Z"),
		time.Now().Add(25*time.Hour).UTC().Format("20060102T150405Z"))
	icsPath := filepath.Join(dir, "cal.ics")
	if err := os.WriteFile(icsPath, []byte(ics), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := fmt.Sprintf("socket: %s\nsyncers:\n  - type: ics\n    name: test\n    path: %s\n    interval: 1h\n    labels: [calendar]\n", sock, icsPath)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, Options{Dir: dir, Listen: addr}) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run returned %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Errorf("daemon did not shut down")
		}
	}()

	waitHealthy(t, "http://"+addr+"/healthz")

	// TCP client: create and read back.
	tc := client.New("http://" + addr)
	created, err := tc.CreateTask(ctx, connect.NewRequest(&taskpb.CreateTaskRequest{Title: "hello"}))
	if err != nil {
		t.Fatalf("CreateTask over TCP: %v", err)
	}

	// Unix-socket client sees the same data.
	uc := client.New("unix://" + sock)
	got, err := uc.GetTask(ctx, connect.NewRequest(&taskpb.GetTaskRequest{Id: created.Msg.GetTask().GetId()}))
	if err != nil || got.Msg.GetTask().GetTitle() != "hello" {
		t.Fatalf("GetTask over unix socket: %v, %v", got, err)
	}

	// The syncer's first pass lands the calendar event as a synced task.
	source := "ics:test"
	deadline := time.Now().Add(10 * time.Second)
	for {
		res, err := tc.ListTasks(ctx, connect.NewRequest(&taskpb.ListTasksRequest{
			Filter: &taskpb.TaskFilter{Source: &source},
		}))
		if err != nil {
			t.Fatalf("ListTasks: %v", err)
		}
		if tasks := res.Msg.GetTasks(); len(tasks) == 1 {
			tk := tasks[0]
			if tk.GetTitle() != "Dentist" || len(tk.GetLabels()) != 1 || tk.GetLabels()[0] != "calendar" {
				t.Fatalf("synced task = %v", tk)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ICS syncer never imported the event")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func waitHealthy(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		res, err := http.Get(url)
		if err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("daemon never became healthy at %s", url)
}
