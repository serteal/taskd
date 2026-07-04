package daemon

import (
	"context"
	"fmt"
	"io"
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
// listeners, and an extension whose syncer is a shell script that talks
// back through the public JSON API — and drives it from outside only.
func TestDaemonEndToEnd(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "taskd.sock")
	addr := freeAddr(t)

	cfg := fmt.Sprintf("socket: %s\n", sock)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	// A complete extension: a syncer (curl over Connect's JSON protocol —
	// proving a syncer can be anything) and a web half served at /ext/.
	extDir := filepath.Join(dir, "extensions", "mock")
	if err := os.MkdirAll(filepath.Join(extDir, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name": "mock", "syncer": ["/bin/sh", "./sync.sh"], "web": true}`
	sync := `#!/bin/sh
curl -s -X POST "$TASKD_ADDR/task.TaskService/UpsertExternalTasks" \
  -H 'content-type: application/json' \
  -d '{"source":"mock:test","tasks":[{"externalRef":"m1","title":"Mock synced"}],"applyLabels":["mock"],"fullSnapshot":true}' >/dev/null
exec sleep 300
`
	for name, content := range map[string]string{
		"manifest.json": manifest,
		"sync.sh":       sync,
		"web/main.js":   "export default { name: 'mock', register() {} };\n",
	} {
		if err := os.WriteFile(filepath.Join(extDir, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
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

	// The supervised syncer lands its task through the public API.
	source := "mock:test"
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
			if tk.GetTitle() != "Mock synced" || len(tk.GetLabels()) != 1 || tk.GetLabels()[0] != "mock" {
				t.Fatalf("synced task = %v", tk)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("extension syncer never imported the task")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The web half is discoverable and served — and ONLY web/ is served.
	if body := httpGet(t, "http://"+addr+"/ext/index.json"); body != `[{"name":"mock"}]` {
		t.Errorf("/ext/index.json = %s", body)
	}
	if body := httpGet(t, "http://"+addr+"/ext/mock/main.js"); body == "" {
		t.Errorf("extension bundle not served")
	}
	if res, err := http.Get("http://" + addr + "/ext/mock/sync.sh"); err != nil || res.StatusCode != http.StatusNotFound {
		t.Errorf("extension non-web files must not be served (got %v %v)", res.StatusCode, err)
	}
}

func httpGet(t *testing.T, url string) string {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %d", url, res.StatusCode)
	}
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
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
