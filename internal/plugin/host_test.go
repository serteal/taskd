package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
	"todoapp/internal/secret"
)

// testBin is the real plugin binary, built once in TestMain from
// testdata/testplugin.
var testBin string

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "tpb")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	testBin = filepath.Join(tmp, "testplugin")
	if out, err := exec.Command("go", "build", "-o", testBin, "./testdata/testplugin").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build testplugin: %v\n%s", err, out)
		os.RemoveAll(tmp)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

// shortRuntimeDir returns a socket-safe short directory: t.TempDir() paths
// can blow the 104-byte unix socket cap on macOS.
func shortRuntimeDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ph")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func newFileSecrets(t *testing.T) secret.Store {
	t.Helper()
	s, err := secret.Open("file", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newTestHost(t *testing.T, secrets secret.Store) *Host {
	t.Helper()
	return NewHost(HostOptions{
		RuntimeDir:     shortRuntimeDir(t),
		Secrets:        secrets,
		Log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		DialTimeout:    5 * time.Second,
		StopGrace:      2 * time.Second,
		BackoffInitial: 50 * time.Millisecond,
		BackoffCap:     200 * time.Millisecond,
		BackoffReset:   time.Minute,
	})
}

// writeScript points TESTPLUGIN_SCRIPT at a fresh JSON script file.
func writeScript(t *testing.T, s map[string]any) {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TESTPLUGIN_SCRIPT", path)
}

func item(id, title string) map[string]any {
	return map[string]any{"external_id": id, "kind": "test.item", "title": title}
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// usePidFile makes the test plugin write its pid at process startup.
func usePidFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pid")
	t.Setenv("TESTPLUGIN_PID_FILE", path)
	return path
}

func readPid(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	pid, err := strconv.Atoi(string(b))
	if err != nil {
		t.Fatalf("parse pid %q: %v", b, err)
	}
	return pid
}

func processGone(pid int) bool {
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

func TestHappyPath(t *testing.T) {
	writeScript(t, map[string]any{
		"items":        []any{item("e1", "One"), item("e2", "Two")},
		"poll_seconds": 45,
	})
	h := newTestHost(t, newFileSecrets(t))

	var registered atomic.Int32
	r, err := h.Start(context.Background(), Instance{Name: "cal@a", Binary: testBin}, func(m *pluginv1.Manifest) error {
		if m.GetName() != "testplugin" {
			return fmt.Errorf("unexpected manifest name %q", m.GetName())
		}
		registered.Add(1)
		return nil
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer r.Stop()

	if got := registered.Load(); got != 1 {
		t.Fatalf("registerManifest ran %d times, want 1", got)
	}
	if got := r.Manifest().GetName(); got != "testplugin" {
		t.Fatalf("Manifest().Name = %q", got)
	}
	if got := r.Capabilities().GetEnumeration(); got != pluginv1.Enumeration_ENUMERATION_SNAPSHOT {
		t.Fatalf("Capabilities().Enumeration = %v", got)
	}
	if got := r.Poll(); got != 45*time.Second {
		t.Fatalf("Poll() = %v, want 45s (capability hint)", got)
	}

	src := r.Source()
	if got := src.Instance(); got != "cal@a" {
		t.Fatalf("Source().Instance() = %q", got)
	}
	items, err := src.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(items) != 2 || items[0].GetExternalId() != "e1" || items[1].GetTitle() != "Two" {
		t.Fatalf("Snapshot = %v", items)
	}
}

func TestResolvePoll(t *testing.T) {
	cases := []struct {
		configured time.Duration
		hint       *durationpb.Duration
		want       time.Duration
	}{
		{0, nil, 5 * time.Minute},                                            // no config, no hint → default
		{0, durationpb.New(45 * time.Second), 45 * time.Second},              // hint wins when unconfigured
		{0, durationpb.New(10 * time.Second), 30 * time.Second},              // hint floored
		{2 * time.Minute, durationpb.New(45 * time.Second), 2 * time.Minute}, // config wins
		{10 * time.Second, nil, 30 * time.Second},                            // config floored
		{0, durationpb.New(0), 5 * time.Minute},                              // zero hint → default
	}
	for _, c := range cases {
		if got := resolvePoll(c.configured, c.hint); got != c.want {
			t.Errorf("resolvePoll(%v, %v) = %v, want %v", c.configured, c.hint.AsDuration(), got, c.want)
		}
	}
}

func TestRegisterManifestErrorAborts(t *testing.T) {
	pidFile := usePidFile(t)
	h := newTestHost(t, newFileSecrets(t))

	boom := errors.New("breaking manifest change")
	r, err := h.Start(context.Background(), Instance{Name: "cal@a", Binary: testBin}, func(*pluginv1.Manifest) error {
		return boom
	})
	if err == nil {
		r.Stop()
		t.Fatal("Start: want error from registerManifest, got nil")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("Start error = %v, want wrapped %v", err, boom)
	}
	// The half-started process must be killed.
	pid := readPid(t, pidFile)
	waitFor(t, 5*time.Second, "aborted plugin process to die", func() bool { return processGone(pid) })
}

func TestCrashRestart(t *testing.T) {
	writeScript(t, map[string]any{"items": []any{item("e1", "One")}})
	pidFile := usePidFile(t)
	marker := filepath.Join(t.TempDir(), "crashed")
	h := newTestHost(t, newFileSecrets(t))

	var registered atomic.Int32
	r, err := h.Start(context.Background(), Instance{
		Name:   "cal@a",
		Binary: testBin,
		Config: map[string]any{"crash_after_configure": true, "crash_marker": marker},
	}, func(*pluginv1.Manifest) error {
		registered.Add(1)
		return nil
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer r.Stop()

	firstPid := readPid(t, pidFile)
	// The plugin exits ~150ms after Configure; supervision restarts it.
	waitFor(t, 10*time.Second, "supervised restart (new pid)", func() bool {
		b, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		pid, err := strconv.Atoi(string(b))
		return err == nil && pid != firstPid
	})

	// The same Source keeps working after the restart.
	src := r.Source()
	var items []*pluginv1.RemoteItem
	waitFor(t, 10*time.Second, "Source to recover after restart", func() bool {
		var err error
		items, err = src.Snapshot(context.Background())
		return err == nil
	})
	if len(items) != 1 || items[0].GetExternalId() != "e1" {
		t.Fatalf("Snapshot after restart = %v", items)
	}
	if got := registered.Load(); got < 2 {
		t.Fatalf("registerManifest ran %d times, want >= 2 (re-registered on restart)", got)
	}
}

func TestStopTerminates(t *testing.T) {
	pidFile := usePidFile(t)
	h := newTestHost(t, newFileSecrets(t))
	r, err := h.Start(context.Background(), Instance{Name: "cal@a", Binary: testBin}, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := readPid(t, pidFile)

	r.Stop()
	waitFor(t, 5*time.Second, "plugin process to die after Stop", func() bool { return processGone(pid) })

	if _, err := r.Source().Snapshot(context.Background()); err == nil {
		t.Fatal("Snapshot after Stop: want error, got nil")
	}
	r.Stop() // idempotent
}

func TestCorehostSecretRoundTrip(t *testing.T) {
	store := newFileSecrets(t)
	h := newTestHost(t, store)
	// The test plugin Puts the secret via the corehost socket during
	// Configure, then Gets it back and fails Configure on mismatch — so a
	// successful Start already proves the plugin-side round trip.
	r, err := h.Start(context.Background(), Instance{
		Name:   "cal@a",
		Binary: testBin,
		Config: map[string]any{"store_secret": "token=s3cr3t"},
	}, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer r.Stop()

	v, found, err := store.Get("cal@a/token")
	if err != nil || !found {
		t.Fatalf("store.Get(cal@a/token): found=%v err=%v", found, err)
	}
	if string(v) != "s3cr3t" {
		t.Fatalf("secret = %q, want s3cr3t", v)
	}
	// The un-namespaced key must not exist.
	if _, found, _ := store.Get("token"); found {
		t.Fatal("secret stored without instance namespace")
	}
}

func TestTwoInstancesIsolateSecrets(t *testing.T) {
	store := newFileSecrets(t)
	h := newTestHost(t, store)
	ctx := context.Background()

	r1, err := h.Start(ctx, Instance{
		Name:   "a@1",
		Binary: testBin,
		Config: map[string]any{"store_secret": "token=va"},
	}, nil)
	if err != nil {
		t.Fatalf("Start a@1: %v", err)
	}
	defer r1.Stop()

	r2, err := h.Start(ctx, Instance{
		Name:   "b@1",
		Binary: testBin,
		Config: map[string]any{"store_secret": "token=vb"},
	}, nil)
	if err != nil {
		t.Fatalf("Start b@1: %v", err)
	}
	defer r2.Stop()

	va, found, err := store.Get("a@1/token")
	if err != nil || !found || string(va) != "va" {
		t.Fatalf("a@1/token = %q found=%v err=%v, want va", va, found, err)
	}
	vb, found, err := store.Get("b@1/token")
	if err != nil || !found || string(vb) != "vb" {
		t.Fatalf("b@1/token = %q found=%v err=%v, want vb", vb, found, err)
	}
	if r1.Source().Instance() == r2.Source().Instance() {
		t.Fatal("instances share a source namespace")
	}
}

func TestContextCancelStopsInstance(t *testing.T) {
	pidFile := usePidFile(t)
	h := newTestHost(t, newFileSecrets(t))
	ctx, cancel := context.WithCancel(context.Background())
	r, err := h.Start(ctx, Instance{Name: "cal@a", Binary: testBin}, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := readPid(t, pidFile)

	cancel()
	waitFor(t, 5*time.Second, "plugin process to die after ctx cancel", func() bool { return processGone(pid) })
	r.Stop() // still safe after ctx-driven shutdown
}

func TestSecretServerNamespacing(t *testing.T) {
	store := newFileSecrets(t)
	s := &secretServer{ns: "x@1", store: store}
	ctx := context.Background()

	if _, err := s.PutSecret(ctx, &pluginv1.PutSecretRequest{Key: "k", Value: []byte("v")}); err != nil {
		t.Fatalf("PutSecret: %v", err)
	}
	if v, found, err := store.Get("x@1/k"); err != nil || !found || string(v) != "v" {
		t.Fatalf("store key x@1/k = %q found=%v err=%v", v, found, err)
	}

	got, err := s.GetSecret(ctx, &pluginv1.GetSecretRequest{Key: "k"})
	if err != nil || !got.GetFound() || string(got.GetValue()) != "v" {
		t.Fatalf("GetSecret: %v found=%v value=%q", err, got.GetFound(), got.GetValue())
	}
	absent, err := s.GetSecret(ctx, &pluginv1.GetSecretRequest{Key: "nope"})
	if err != nil || absent.GetFound() {
		t.Fatalf("GetSecret absent: err=%v found=%v", err, absent.GetFound())
	}

	if _, err := s.DeleteSecret(ctx, &pluginv1.DeleteSecretRequest{Key: "k"}); err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}
	if _, err := s.DeleteSecret(ctx, &pluginv1.DeleteSecretRequest{Key: "k"}); err != nil {
		t.Fatalf("DeleteSecret idempotent: %v", err)
	}
	if _, err := s.PutSecret(ctx, &pluginv1.PutSecretRequest{Key: ""}); err == nil ||
		!strings.Contains(err.Error(), "key required") {
		t.Fatalf("PutSecret empty key: err=%v, want InvalidArgument", err)
	}
}
