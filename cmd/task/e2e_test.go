package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/daemon"
	"todoapp/internal/server"
)

// NOTE: test names are deliberately short — t.TempDir() feeds the unix
// socket path, and macOS caps those at 104 bytes.

// startDaemon runs taskd in-process against a fresh temp dir and returns
// the dir; the daemon stops (and its error is checked) at cleanup.
func startDaemon(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
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
	return dir
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition never became true")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// run executes one CLI invocation with a fresh cobra root, captured output.
func run(t *testing.T, dir string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := newRootCmd()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"--dir", dir}, args...))
	err = root.ExecuteContext(context.Background())
	return out.String(), errOut.String(), err
}

func mustRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, errOut, err := run(t, dir, args...)
	if err != nil {
		t.Fatalf("task %s: %v\nstderr: %s", strings.Join(args, " "), err, errOut)
	}
	return out
}

// addItem creates an item and returns its 10-char id prefix from the
// confirmation line ("added <shortID> <title>"). Ten chars cover the ULID's
// full millisecond timestamp, so sequential CLI adds get unique prefixes.
func addItem(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out := mustRun(t, dir, append([]string{"add"}, args...)...)
	fields := strings.Fields(out)
	if len(fields) < 2 || fields[0] != "added" || len(fields[1]) != 10 {
		t.Fatalf("add confirmation %q: want `added <10-char id> <title>`", out)
	}
	return fields[1]
}

// addItemFull creates an item via --json and returns the FULL id, for tests
// that must address one specific item regardless of prefix behavior.
func addItemFull(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out := mustRun(t, dir, append([]string{"--json", "add"}, args...)...)
	it := &taskcorev1.Item{}
	if err := protojson.Unmarshal([]byte(strings.TrimSpace(out)), it); err != nil {
		t.Fatalf("add --json output %q: %v", out, err)
	}
	return it.GetId()
}

// rows counts non-header lines of an ls table.
func rows(out string) int {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) <= 1 {
		return 0
	}
	return len(lines) - 1
}

func TestAddLs(t *testing.T) {
	dir := startDaemon(t)
	id := addItem(t, dir, "pay rent", "-p", "home", "-l", "bills")

	out := mustRun(t, dir, "ls")
	if !strings.Contains(out, "pay rent") || !strings.Contains(out, id) {
		t.Fatalf("ls output missing the new item:\n%s", out)
	}
	if !strings.Contains(out, "home") || !strings.Contains(out, "bills") {
		t.Fatalf("ls output missing project/labels:\n%s", out)
	}

	// --json emits one protojson item per line.
	jsonOut := mustRun(t, dir, "--json", "ls")
	line := strings.SplitN(strings.TrimSpace(jsonOut), "\n", 2)[0]
	it := &taskcorev1.Item{}
	if err := protojson.Unmarshal([]byte(line), it); err != nil {
		t.Fatalf("ls --json line %q: %v", line, err)
	}
	if it.GetTodo().GetTitleOverride() != "pay rent" {
		t.Fatalf("ls --json title = %q, want pay rent", it.GetTodo().GetTitleOverride())
	}
}

func TestDoneFlow(t *testing.T) {
	dir := startDaemon(t)
	id := addItem(t, dir, "write report")

	out := mustRun(t, dir, "done", id, "--reason", "shipped")
	if !strings.Contains(out, "completed "+id) {
		t.Fatalf("done confirmation %q", out)
	}
	if out := mustRun(t, dir, "ls"); strings.Contains(out, "write report") {
		t.Fatalf("completed item still in default ls:\n%s", out)
	}
	arch := mustRun(t, dir, "ls", "--completed")
	if !strings.Contains(arch, "✓ write report") {
		t.Fatalf("ls --completed must show the item with a ✓:\n%s", arch)
	}
	show := mustRun(t, dir, "show", id)
	if !strings.Contains(show, "reason: shipped") {
		t.Fatalf("show must include the completion reason:\n%s", show)
	}

	mustRun(t, dir, "reopen", id)
	if out := mustRun(t, dir, "ls"); !strings.Contains(out, "write report") {
		t.Fatalf("reopen must restore the item to default ls:\n%s", out)
	}
}

func TestDue(t *testing.T) {
	dir := startDaemon(t)
	id := addItem(t, dir, "book flight")
	mustRun(t, dir, "edit", id, "--due", "tomorrow")

	out := mustRun(t, dir, "ls")
	if !regexp.MustCompile(`book flight.*\b1d\b`).MatchString(out) {
		t.Fatalf("ls must show a 1d due column after `edit --due tomorrow`:\n%s", out)
	}

	mustRun(t, dir, "edit", id, "--clear-due")
	if out := mustRun(t, dir, "ls"); regexp.MustCompile(`book flight.*1d`).MatchString(out) {
		t.Fatalf("--clear-due must empty the due column:\n%s", out)
	}
}

func TestLabelRT(t *testing.T) {
	dir := startDaemon(t)
	id := addItem(t, dir, "tidy desk")

	mustRun(t, dir, "edit", id, "--add-label", "urgent", "--add-label", "home")
	show := mustRun(t, dir, "show", id)
	if !strings.Contains(show, "urgent, home") {
		t.Fatalf("labels after add: %s", show)
	}

	mustRun(t, dir, "edit", id, "--rm-label", "urgent")
	show = mustRun(t, dir, "show", id)
	if strings.Contains(show, "urgent") || !strings.Contains(show, "home") {
		t.Fatalf("labels after rm: %s", show)
	}
}

func TestSnooze(t *testing.T) {
	dir := startDaemon(t)
	id := addItem(t, dir, "call bank")

	mustRun(t, dir, "edit", id, "--snooze", "12h")
	if out := mustRun(t, dir, "ls"); strings.Contains(out, "call bank") {
		t.Fatalf("snoozed item must hide from default ls:\n%s", out)
	}
	if out := mustRun(t, dir, "ls", "--all"); !strings.Contains(out, "call bank") {
		t.Fatalf("ls --all must still show the snoozed item:\n%s", out)
	}
	mustRun(t, dir, "edit", id, "--clear-snooze")
	if out := mustRun(t, dir, "ls"); !strings.Contains(out, "call bank") {
		t.Fatalf("clearing the snooze must bring the item back:\n%s", out)
	}
}

func TestView(t *testing.T) {
	dir := startDaemon(t)
	addItem(t, dir, "hot thing", "-l", "urgent")
	addItem(t, dir, "cold thing")

	mustRun(t, dir, "view", "save", "hot", "-f", `"urgent" in labels`, "--order-by", "due", "--description", "on fire")
	out := mustRun(t, dir, "ls", "hot")
	if !strings.Contains(out, "hot thing") || strings.Contains(out, "cold thing") {
		t.Fatalf("ls VIEW must apply the view's filter:\n%s", out)
	}
	if vl := mustRun(t, dir, "view", "ls"); !strings.Contains(vl, "hot") || !strings.Contains(vl, "on fire") {
		t.Fatalf("view ls:\n%s", vl)
	}
	mustRun(t, dir, "view", "rm", "hot")
	if _, _, err := run(t, dir, "ls", "hot"); err == nil {
		t.Fatal("ls of a deleted view must fail")
	}
}

func TestCounts(t *testing.T) {
	dir := startDaemon(t)
	addItem(t, dir, "a", "-l", "x", "-l", "y", "-p", "p1")
	addItem(t, dir, "b", "-l", "x", "-p", "p1")
	addItem(t, dir, "c", "-p", "p2")

	labels := mustRun(t, dir, "labels")
	for _, want := range []string{`x\s+2`, `y\s+1`} {
		if !regexp.MustCompile(want).MatchString(labels) {
			t.Fatalf("labels output missing %s:\n%s", want, labels)
		}
	}
	projects := mustRun(t, dir, "projects")
	for _, want := range []string{`p1\s+2`, `p2\s+1`} {
		if !regexp.MustCompile(want).MatchString(projects) {
			t.Fatalf("projects output missing %s:\n%s", want, projects)
		}
	}
}

// TestPortab: export from one daemon, import into a second fresh one, and
// the item sets must match (ids change; completion survives).
func TestPortab(t *testing.T) {
	dirA := startDaemon(t)
	addItem(t, dirA, "alpha", "-p", "work", "-l", "x")
	idB := addItemFull(t, dirA, "beta")
	mustRun(t, dirA, "done", idB, "--reason", "wontdo")
	mustRun(t, dirA, "view", "save", "mine", "-f", `project == "work"`)

	file := filepath.Join(t.TempDir(), "e.jsonl")
	mustRun(t, dirA, "export", "-o", file)

	dirB := startDaemon(t)
	out := mustRun(t, dirB, "import", "-i", file)
	if !strings.Contains(out, "imported 2 items") {
		t.Fatalf("import summary %q, want 2 items", out)
	}

	lsA := mustRun(t, dirA, "ls", "--all")
	lsB := mustRun(t, dirB, "ls", "--all")
	if rows(lsA) != rows(lsB) {
		t.Fatalf("item counts differ after import: A=%d B=%d\nA:\n%s\nB:\n%s", rows(lsA), rows(lsB), lsA, lsB)
	}
	for _, want := range []string{"alpha", "✓ beta", "work", "x"} {
		if !strings.Contains(lsB, want) {
			t.Fatalf("imported ls --all missing %q:\n%s", want, lsB)
		}
	}
	if vl := mustRun(t, dirB, "view", "ls"); !strings.Contains(vl, "mine") {
		t.Fatalf("imported views missing 'mine':\n%s", vl)
	}
}

func TestRm(t *testing.T) {
	dir := startDaemon(t)
	id := addItem(t, dir, "oops")

	out := mustRun(t, dir, "rm", id)
	if !strings.Contains(out, "deleted "+id) {
		t.Fatalf("rm confirmation %q", out)
	}
	if out := mustRun(t, dir, "ls", "--all"); strings.Contains(out, "oops") {
		t.Fatalf("deleted item still listed:\n%s", out)
	}
	if _, _, err := run(t, dir, "rm", id); err == nil {
		t.Fatal("removing a removed item must fail")
	}
}

// TestAmbig: two items share the ULID timestamp prefix, so a 1-char id must
// be rejected with a helpful message.
func TestAmbig(t *testing.T) {
	dir := startDaemon(t)
	addItem(t, dir, "first")
	addItem(t, dir, "second")

	_, _, err := run(t, dir, "show", "0")
	if err == nil {
		t.Fatal("1-char prefix matching two items must fail")
	}
	if !strings.Contains(err.Error(), "ambiguous") || !strings.Contains(err.Error(), "more characters") {
		t.Fatalf("ambiguous-prefix error %q must tell the user what to do", err)
	}
}

// lockedBuf is a Writer safe to read while another goroutine writes.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestWatchCmd(t *testing.T) {
	dir := startDaemon(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out := &lockedBuf{}
	root := newRootCmd()
	root.SetOut(out)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"--dir", dir, "watch"})
	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()

	// The watch anchors "from now", so keep adding until one lands after it.
	seen := false
	for i := 0; i < 20 && !seen; i++ {
		addItem(t, dir, fmt.Sprintf("ping %d", i))
		deadline := time.Now().Add(500 * time.Millisecond)
		for time.Now().Before(deadline) {
			if strings.Contains(out.String(), "CREATED") {
				seen = true
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("watch exited with error: %v", err)
	}
	if !seen {
		t.Fatalf("watch never printed a CREATED event; output:\n%s", out.String())
	}
	if !regexp.MustCompile(`\d{2}:\d{2}:\d{2} CREATED \w{10} ping`).MatchString(out.String()) {
		t.Fatalf("watch line format unexpected:\n%s", out.String())
	}
}

func TestStatus(t *testing.T) {
	dir := startDaemon(t)
	out := mustRun(t, dir, "daemon", "status")
	if strings.Contains(out, "not running") || !strings.Contains(out, server.Version) {
		t.Fatalf("daemon status against a live daemon = %q", out)
	}

	empty := t.TempDir()
	if out := mustRun(t, empty, "daemon", "status"); !strings.Contains(out, "not running") {
		t.Fatalf("daemon status without a daemon = %q", out)
	}
}

// TestBinaries builds the real task and taskd binaries and drives them via
// os/exec end to end.
func TestBinaries(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: skipping binary build")
	}
	moduleRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	for _, target := range [][2]string{{"task", "./cmd/task"}, {"taskd", "./cmd/taskd"}} {
		cmd := exec.Command("go", "build", "-o", filepath.Join(bin, target[0]), target[1])
		cmd.Dir = moduleRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build %s: %v\n%s", target[1], err, out)
		}
	}

	data := t.TempDir()
	taskd := exec.Command(filepath.Join(bin, "taskd"), "--dir", data)
	taskd.Stdout, taskd.Stderr = io.Discard, io.Discard
	if err := taskd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = taskd.Process.Signal(syscall.SIGTERM)
		_ = taskd.Wait()
	}()
	waitFor(t, 5*time.Second, func() bool {
		_, err := os.Stat(daemon.SocketPath(data))
		return err == nil
	})

	task := filepath.Join(bin, "task")
	addOut, err := exec.Command(task, "--dir", data, "add", "hello world").CombinedOutput()
	if err != nil {
		t.Fatalf("task add: %v\n%s", err, addOut)
	}
	if !strings.Contains(string(addOut), "added") {
		t.Fatalf("task add output %q", addOut)
	}
	lsOut, err := exec.Command(task, "--dir", data, "ls").CombinedOutput()
	if err != nil {
		t.Fatalf("task ls: %v\n%s", err, lsOut)
	}
	if !strings.Contains(string(lsOut), "hello world") {
		t.Fatalf("task ls output %q", lsOut)
	}
}
