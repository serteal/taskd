package daemon

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"

	adminpb "github.com/serteal/taskd/gen/admin"
	"github.com/serteal/taskd/gen/admin/adminconnect"
	taskpb "github.com/serteal/taskd/gen/task"
	"github.com/serteal/taskd/pkg/client"
)

// TestDaemonDisabledExtension boots the daemon with an extension listed under
// extensions.disabled and proves the whole disable→enable loop end to end: at
// startup the extension is neither supervised nor served, then the admin API
// hot-enables it (no restart) — the syncer starts importing and the bundle
// starts serving — and the change is persisted back to config.yaml.
func TestDaemonDisabledExtension(t *testing.T) {
	dir := t.TempDir()
	addr := freeAddr(t)

	if err := os.WriteFile(filepath.Join(dir, "config.yaml"),
		[]byte("extensions:\n  disabled: [mock]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	extDir := filepath.Join(dir, "extensions", "mock")
	if err := os.MkdirAll(filepath.Join(extDir, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	sync := `#!/bin/sh
curl -s -X POST "$TASKD_ADDR/task.TaskService/UpsertExternalTasks" \
  -H 'content-type: application/json' \
  -d '{"source":"mock:test","tasks":[{"externalRef":"m1","title":"Mock synced"}],"applyLabels":["mock"],"fullSnapshot":true}' >/dev/null
exec sleep 300
`
	for name, content := range map[string]string{
		"manifest.json": `{"name": "mock", "syncer": ["/bin/sh", "./sync.sh"], "web": true}`,
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

	// Disabled at startup: excluded from the index and its bundle 404s.
	if body := httpGet(t, "http://"+addr+"/ext/index.json"); body != `[]` {
		t.Errorf("/ext/index.json = %s, want [] (mock disabled)", body)
	}
	if res, _ := http.Get("http://" + addr + "/ext/mock/main.js"); res == nil || res.StatusCode != http.StatusNotFound {
		t.Errorf("disabled extension bundle must 404, got %v", res)
	}

	tc := client.New("http://" + addr)
	source := "mock:test"
	// Not supervised: the syncer has not imported anything.
	if res, err := tc.ListTasks(ctx, connect.NewRequest(&taskpb.ListTasksRequest{
		Filter: &taskpb.TaskFilter{Source: &source},
	})); err != nil || len(res.Msg.GetTasks()) != 0 {
		t.Fatalf("disabled syncer already ran: %v tasks, err=%v", len(res.Msg.GetTasks()), err)
	}

	// Hot-enable through the admin API.
	ac := adminconnect.NewAdminServiceClient(http.DefaultClient, "http://"+addr)
	res, err := ac.SetExtensionEnabled(ctx, connect.NewRequest(&adminpb.SetExtensionEnabledRequest{Name: "mock", Enabled: true}))
	if err != nil {
		t.Fatalf("SetExtensionEnabled: %v", err)
	}
	if res.Msg.GetRestartRequired() {
		t.Errorf("restart_required = true, want hot-apply")
	}

	// Now served.
	if body := httpGet(t, "http://"+addr+"/ext/index.json"); body != `[{"name":"mock"}]` {
		t.Errorf("/ext/index.json = %s, want mock after enable", body)
	}

	// Now supervised: the syncer imports its task.
	deadline := time.Now().Add(10 * time.Second)
	for {
		res, err := tc.ListTasks(ctx, connect.NewRequest(&taskpb.ListTasksRequest{
			Filter: &taskpb.TaskFilter{Source: &source},
		}))
		if err != nil {
			t.Fatalf("ListTasks: %v", err)
		}
		if len(res.Msg.GetTasks()) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("enabled syncer never imported the task")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Persisted: config.yaml no longer disables mock.
	cfg, err := loadFileConfig(dir)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if len(cfg.Extensions.Disabled) != 0 {
		t.Errorf("config still disables %v, want none after enable", cfg.Extensions.Disabled)
	}
}
