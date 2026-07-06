package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubOpenURL replaces the browser launcher for the duration of a test so
// booting the daemon never spawns a real browser, and records the URLs opened.
func stubOpenURL(t *testing.T) *recorder {
	t.Helper()
	rec := &recorder{}
	prev := openURL
	openURL = func(url string) error {
		rec.add(url)
		return nil
	}
	t.Cleanup(func() { openURL = prev })
	return rec
}

type recorder struct {
	mu   sync.Mutex
	urls []string
}

func (r *recorder) add(url string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.urls = append(r.urls, url)
}

func (r *recorder) opened() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.urls...)
}

func boolp(b bool) *bool { return &b }

func TestWantOpen(t *testing.T) {
	cases := []struct {
		name                    string
		first, openFlag, noOpen bool
		cfgOpen                 *bool
		want                    bool
	}{
		{name: "first run, no signals", first: true, want: true},
		{name: "not first run, no signals", first: false, want: false},
		{name: "first run suppressed by flag", first: true, noOpen: true, want: false},
		{name: "first run suppressed by config", first: true, cfgOpen: boolp(false), want: false},
		{name: "explicit flag on later run", first: false, openFlag: true, want: true},
		{name: "config open on later run", first: false, cfgOpen: boolp(true), want: true},
		{name: "explicit flag beats no-open", first: false, openFlag: true, noOpen: true, want: true},
		{name: "config true beats first-run false", first: false, cfgOpen: boolp(true), want: true},
		// -no-open beats config open: true (the flag is more specific).
		{name: "no-open flag beats config true", first: false, cfgOpen: boolp(true), noOpen: true, want: false},
		{name: "no-open flag beats config true on first run", first: true, cfgOpen: boolp(true), noOpen: true, want: false},
		// -open still wins over everything, including config false.
		{name: "open flag beats config false", first: false, openFlag: true, cfgOpen: boolp(false), want: true},
		{name: "both flags open wins", first: false, openFlag: true, noOpen: true, cfgOpen: boolp(false), want: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := wantOpen(c.first, c.openFlag, c.noOpen, c.cfgOpen); got != c.want {
				t.Errorf("wantOpen(first=%v, open=%v, noOpen=%v, cfg=%v) = %v, want %v",
					c.first, c.openFlag, c.noOpen, c.cfgOpen, got, c.want)
			}
		})
	}
}

func TestFirstRun(t *testing.T) {
	dir := t.TempDir()
	if !firstRun(dir) {
		t.Errorf("firstRun on an empty dir = false, want true")
	}
	if err := os.WriteFile(filepath.Join(dir, "tasks.db"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if firstRun(dir) {
		t.Errorf("firstRun after tasks.db exists = true, want false")
	}
}

func TestConfigOpenParsing(t *testing.T) {
	for _, c := range []struct {
		body string
		want *bool
	}{
		{"open: true\n", boolp(true)},
		{"open: false\n", boolp(false)},
		{"listen: 127.0.0.1:9000\n", nil}, // unset stays nil
	} {
		dir := t.TempDir()
		write(t, dir, c.body)
		cfg, err := loadFileConfig(dir)
		if err != nil {
			t.Fatalf("loadFileConfig(%q): %v", c.body, err)
		}
		switch {
		case c.want == nil && cfg.Open != nil:
			t.Errorf("%q: Open = %v, want nil", c.body, *cfg.Open)
		case c.want != nil && (cfg.Open == nil || *cfg.Open != *c.want):
			t.Errorf("%q: Open = %v, want %v", c.body, cfg.Open, *c.want)
		}
	}
}

// TestSaveConfigPreservesOpen: writing back the extensions section must not
// disturb an existing open: setting.
func TestSaveConfigPreservesOpen(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "open: true\n")
	if err := saveFileConfig(dir, FileConfig{Extensions: ExtensionsConfig{Disabled: []string{"gcal"}}}); err != nil {
		t.Fatalf("saveFileConfig: %v", err)
	}
	cfg, err := loadFileConfig(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cfg.Open == nil || !*cfg.Open {
		t.Errorf("Open = %v after save, want preserved true", cfg.Open)
	}
	if got := cfg.Extensions.Disabled; len(got) != 1 || got[0] != "gcal" {
		t.Errorf("Disabled = %v, want [gcal]", got)
	}
}

// TestFirstRunAutoOpen: the daemon opens the web app on its first run in a
// fresh data dir, and does not on a later run of the same dir.
func TestFirstRunAutoOpen(t *testing.T) {
	dir := t.TempDir()

	// run boots the daemon and gives the detached launcher a settle window —
	// returning as soon as it fired, or after the grace period so a run that
	// should NOT open is proven not to — then shuts down and reports the opens.
	run := func() []string {
		rec := stubOpenURL(t)
		addr := freeAddr(t)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- Run(ctx, Options{Dir: dir, Listen: addr}) }()
		waitHealthy(t, "http://"+addr+"/healthz")
		deadline := time.Now().Add(500 * time.Millisecond)
		for time.Now().Before(deadline) && len(rec.opened()) == 0 {
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("daemon did not shut down")
		}
		return rec.opened()
	}

	if opened := run(); len(opened) != 1 || !strings.HasPrefix(opened[0], "http://") {
		t.Fatalf("first run opened %v, want exactly one http:// URL", opened)
	}
	if opened := run(); len(opened) != 0 {
		t.Errorf("second run opened %v, want none", opened)
	}
}

// TestNoOpenEnvSuppressesAutoOpen: TASKD_NO_OPEN beats everything, including
// an explicit -open. Harnesses that spawn many first-run daemons (the web e2e
// suite starts one per test) rely on this to never open browser windows.
func TestNoOpenEnvSuppressesAutoOpen(t *testing.T) {
	t.Setenv("TASKD_NO_OPEN", "1")
	rec := stubOpenURL(t)
	addr := freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, Options{Dir: t.TempDir(), Listen: addr, Open: true}) }()
	waitHealthy(t, "http://"+addr+"/healthz")
	time.Sleep(100 * time.Millisecond) // grace window a launcher would need
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("daemon did not shut down")
	}
	if opened := rec.opened(); len(opened) != 0 {
		t.Errorf("opened %v with TASKD_NO_OPEN set, want none", opened)
	}
}
