package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/daemon"
)

// NOTE: short test names on purpose — the daemon dir feeds unix socket paths
// (macOS caps them at ~104 bytes), so the fixture also uses os.MkdirTemp with
// a short prefix rather than t.TempDir().
//
// These tests drive the real todotxt connector subprocess end to end: the one
// write path toward remotes (IntentService + the mirror.title rename route)
// and the outbox CLI. The plugin binary is compiled once per test run and
// shared; every fixture is otherwise independent.

var (
	todotxtBinOnce sync.Once
	todotxtBinPath string
	todotxtBinErr  error
)

// buildTodotxtPlugin compiles the todotxt connector exactly once per test
// binary run and returns the absolute path of the built binary. The build
// takes a couple of seconds; sharing it across scenarios keeps the suite fast.
func buildTodotxtPlugin(t *testing.T) string {
	t.Helper()
	todotxtBinOnce.Do(func() {
		// A stable temp dir NOT tied to any single test's lifetime (t.TempDir
		// would be removed when its owning test ends, orphaning later tests).
		d, err := os.MkdirTemp("", "task-todotxt-plugin")
		if err != nil {
			todotxtBinErr = err
			return
		}
		bin := filepath.Join(d, "todotxt")
		build := exec.Command("go", "build", "-o", bin, "todoapp/plugins/todotxt/cmd")
		build.Stderr = os.Stderr
		if err := build.Run(); err != nil {
			todotxtBinErr = fmt.Errorf("building todotxt plugin: %w", err)
			return
		}
		todotxtBinPath = bin
	})
	if todotxtBinErr != nil {
		t.Fatal(todotxtBinErr)
	}
	return todotxtBinPath
}

// startTodoDaemon brings up an in-process daemon backed by a todotxt instance
// ("td@e2e") over a two-line file, and returns the data dir plus the todo.txt
// path. It blocks until both file lines have mirrored into the inbox.
func startTodoDaemon(t *testing.T) (dir, todoPath string) {
	t.Helper()
	if testing.Short() {
		t.Skip("-short: skips the todotxt plugin build")
	}
	bin := buildTodotxtPlugin(t)

	dir, err := os.MkdirTemp("", "td") // short: unix socket path cap
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	// The config references the plugin by bare name, resolved to
	// <dir>/plugins/todotxt; symlink the shared binary there.
	if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bin, filepath.Join(dir, "plugins", "todotxt")); err != nil {
		t.Fatal(err)
	}

	todoPath = filepath.Join(dir, "todo.txt")
	if err := os.WriteFile(todoPath, []byte("call the bank id:bank\nwater plants\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := fmt.Sprintf("instances:\n  - name: td@e2e\n    plugin: todotxt\n    poll: 30s\n    config:\n      path: %s\n", todoPath)
	if err := os.WriteFile(daemon.ConfigPath(dir), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	go func() { errc <- daemon.Run(ctx, daemon.Config{Dir: dir, Log: log}) }()
	t.Cleanup(func() {
		cancel()
		if err := <-errc; err != nil {
			t.Errorf("daemon exit: %v", err)
		}
	})
	waitFor(t, 5*time.Second, func() bool {
		_, err := os.Stat(daemon.SocketPath(dir))
		return err == nil
	})

	// Both lines mirror in on the first sync cycle (plugin subprocess startup
	// plus a snapshot); allow up to 15s for that under -race.
	waitFor(t, 15*time.Second, func() bool {
		out, _, err := run(t, dir, "ls", "inbox")
		return err == nil && strings.Contains(out, "call the bank") && strings.Contains(out, "water plants")
	})
	return dir, todoPath
}

// mirrorItem returns the mirrored item whose external id is externalID, found
// by scanning `--json ls --all`.
func mirrorItem(t *testing.T, dir, externalID string) *taskcorev1.Item {
	t.Helper()
	out := mustRun(t, dir, "--json", "ls", "--all")
	var found *taskcorev1.Item
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		it := &taskcorev1.Item{}
		if err := protojson.Unmarshal([]byte(line), it); err != nil {
			t.Fatalf("ls --json line %q: %v", line, err)
		}
		if it.GetMirror().GetLink().GetExternalId() == externalID {
			found = it
		}
	}
	if found == nil {
		t.Fatalf("no mirrored item with external_id %q:\n%s", externalID, out)
	}
	return found
}

// TestRename: renaming a tracked item routes to the connector, confirms
// synchronously, rewrites the file line (keeping its id: tag), and the
// confirmation updates the mirror the local read sees.
func TestRename(t *testing.T) {
	dir, todoPath := startTodoDaemon(t)
	bank := mirrorItem(t, dir, "bank")

	out := mustRun(t, dir, "rename", bank.GetId(), "call the bank about the loan")
	if !strings.Contains(out, "remote confirmed") {
		t.Fatalf("rename output %q, want 'remote confirmed'", out)
	}

	data, err := os.ReadFile(todoPath)
	if err != nil {
		t.Fatal(err)
	}
	file := string(data)
	if !strings.Contains(file, "call the bank about the loan") {
		t.Fatalf("todo.txt not rewritten with the new title:\n%s", file)
	}
	if !strings.Contains(file, "id:bank") {
		t.Fatalf("rename dropped the id:bank identity tag:\n%s", file)
	}

	// The confirmation applied remote truth to the mirror synchronously.
	if ls := mustRun(t, dir, "ls", "inbox"); !strings.Contains(ls, "call the bank about the loan") {
		t.Fatalf("ls inbox missing the renamed title:\n%s", ls)
	}
}

// TestWriteback: the full user flow — the plugin ships a writeback rule
// template, the user applies it, and local completion flows back into the
// file as a set_completed intent delivered through the outbox. (The
// template's validity is enforced at plugin registration by the schema
// registry, so `rule template apply` here is exercising the real path.)
func TestWriteback(t *testing.T) {
	dir, todoPath := startTodoDaemon(t)
	bank := mirrorItem(t, dir, "bank")

	out := mustRun(t, dir, "rule", "template", "apply", "todotxt/todotxt-writeback")
	if !strings.Contains(out, "todotxt-writeback") {
		t.Fatalf("template apply output: %q", out)
	}

	// Promote the mirror into a todo, then complete it — the rule fires and
	// dispatches set_completed to the connector.
	mustRun(t, dir, "edit", bank.GetId(), "-p", "money")
	mustRun(t, dir, "done", bank.GetId())

	if !waitTrue(func() bool {
		data, _ := os.ReadFile(todoPath)
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "call the bank") && strings.HasPrefix(line, "x ") {
				return true
			}
		}
		return false
	}) {
		data, _ := os.ReadFile(todoPath)
		t.Fatalf("set_completed never wrote 'x ' to the bank line:\n%s", string(data))
	}

	// The delivered intent lands CONFIRMED in the full outbox view.
	if !waitTrue(func() bool {
		out, _, err := run(t, dir, "pending", "--all")
		return err == nil && strings.Contains(out, "set_completed") && strings.Contains(out, "CONFIRMED")
	}) {
		t.Fatalf("pending --all never showed a CONFIRMED set_completed row:\n%s", mustRun(t, dir, "pending", "--all"))
	}
}

// TestDiscard covers the intent error path: discarding a nonexistent intent id
// returns a clean NotFound. Offline queuing (an intent that parks QUEUED/FAILED
// because the remote is down) is deliberately NOT exercised at the e2e level —
// simulating remote failure here (chmod tricks, missing-binary instances) is
// flaky across environments, and it is already covered by the router's unit
// tests (TestTransientRetryThenFail).
func TestDiscard(t *testing.T) {
	dir, _ := startTodoDaemon(t)

	_, _, err := run(t, dir, "discard", "01NONEXISTENTINTENTID000000")
	if err == nil {
		t.Fatal("discard of a nonexistent intent must fail")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("discard error %q, want a clean NotFound", err)
	}
}

// TestLink: everything in the todo.txt file is already in the instance's sync
// scope (a whole-file connector tracks every line), so ref "bank" is ALREADY
// tracked — LinkItem must refuse with AlreadyExists, naming the item that
// mirrors it. A clean LinkItem success needs an out-of-scope resolvable object,
// which a whole-file connector cannot produce; that success path is covered at
// unit level by the pinned-sync test. So here we assert only the error path.
func TestLink(t *testing.T) {
	dir, _ := startTodoDaemon(t)
	bank := mirrorItem(t, dir, "bank")

	native := addItem(t, dir, "loan paperwork")

	_, _, err := run(t, dir, "link", native, "td@e2e", "bank")
	if err == nil {
		t.Fatal("linking to an already-tracked remote must fail with AlreadyExists")
	}
	if !strings.Contains(err.Error(), "already tracked") {
		t.Fatalf("link error %q, want an AlreadyExists error", err)
	}
	if !strings.Contains(err.Error(), bank.GetId()) {
		t.Fatalf("link error %q must name the item %s that already mirrors 'bank'", err, bank.GetId())
	}
}

// TestPending checks the outbox table shape. Like scenario 5's "after scenario
// 2", it needs a terminal (CONFIRMED) intent present; a confirmed rename
// produces one directly, without the writeback-rule machinery.
func TestPending(t *testing.T) {
	dir, _ := startTodoDaemon(t)
	bank := mirrorItem(t, dir, "bank")

	if out := mustRun(t, dir, "rename", bank.GetId(), "renamed bank"); !strings.Contains(out, "remote confirmed") {
		t.Fatalf("rename not confirmed: %q", out)
	}

	all := mustRun(t, dir, "pending", "--all")
	for _, want := range []string{"ID", "ITEM", "INTENT", "STATE", "rename", "CONFIRMED"} {
		if !strings.Contains(all, want) {
			t.Fatalf("pending --all missing %q:\n%s", want, all)
		}
	}

	// The live outbox holds only QUEUED/INFLIGHT/FAILED; the rename confirmed,
	// so nothing is live.
	if live := mustRun(t, dir, "pending"); !strings.Contains(live, "outbox is empty") {
		t.Fatalf("pending (live) = %q, want 'outbox is empty'", live)
	}
}
