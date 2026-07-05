package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadConfigDisabled: the extensions.disabled list parses, and listen
// still defaults when the file omits it.
func TestLoadConfigDisabled(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "extensions:\n  disabled: [github, ics]\n")

	cfg, err := loadFileConfig(dir)
	if err != nil {
		t.Fatalf("loadFileConfig: %v", err)
	}
	if cfg.Listen != DefaultListen {
		t.Errorf("Listen = %q, want default %q", cfg.Listen, DefaultListen)
	}
	if got := cfg.Extensions.Disabled; len(got) != 2 || got[0] != "github" || got[1] != "ics" {
		t.Errorf("Disabled = %v, want [github ics]", got)
	}
}

// TestSaveConfigRoundTrip: saveFileConfig persists only the extensions
// section, preserving an explicit listen/socket exactly and never
// materializing the default listen when the file omitted it.
func TestSaveConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	// User's file: explicit listen + socket, no extensions section.
	write(t, dir, "listen: 0.0.0.0:9999\nsocket: /tmp/taskd.sock\n")

	if err := saveFileConfig(dir, FileConfig{Extensions: ExtensionsConfig{Disabled: []string{"github"}}}); err != nil {
		t.Fatalf("saveFileConfig: %v", err)
	}

	cfg, err := loadFileConfig(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cfg.Listen != "0.0.0.0:9999" {
		t.Errorf("Listen = %q, want the user's explicit value preserved", cfg.Listen)
	}
	if cfg.Socket != "/tmp/taskd.sock" {
		t.Errorf("Socket = %q, want preserved", cfg.Socket)
	}
	if got := cfg.Extensions.Disabled; len(got) != 1 || got[0] != "github" {
		t.Errorf("Disabled = %v, want [github]", got)
	}

	// The raw bytes must not contain a materialized default listen for a file
	// that never had one: re-enabling everything clears disabled, and the
	// explicit listen still survives.
	if err := saveFileConfig(dir, FileConfig{Extensions: ExtensionsConfig{}}); err != nil {
		t.Fatalf("saveFileConfig (clear): %v", err)
	}
	cfg, err = loadFileConfig(dir)
	if err != nil {
		t.Fatalf("reload after clear: %v", err)
	}
	if cfg.Listen != "0.0.0.0:9999" || cfg.Socket != "/tmp/taskd.sock" {
		t.Errorf("after clear: listen=%q socket=%q, want both preserved", cfg.Listen, cfg.Socket)
	}
	if len(cfg.Extensions.Disabled) != 0 {
		t.Errorf("Disabled = %v, want empty after clear", cfg.Extensions.Disabled)
	}
}

// TestSaveConfigNoFile: saving with no pre-existing config.yaml writes just
// the extensions section — no defaulted listen leaks in.
func TestSaveConfigNoFile(t *testing.T) {
	dir := t.TempDir()
	if err := saveFileConfig(dir, FileConfig{Extensions: ExtensionsConfig{Disabled: []string{"gcal"}}}); err != nil {
		t.Fatalf("saveFileConfig: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if contains(string(b), "listen:") {
		t.Errorf("config.yaml materialized a default listen:\n%s", b)
	}
	cfg, err := loadFileConfig(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	// Still defaults on load even though the file has no listen line.
	if cfg.Listen != DefaultListen {
		t.Errorf("Listen = %q, want default %q", cfg.Listen, DefaultListen)
	}
	if got := cfg.Extensions.Disabled; len(got) != 1 || got[0] != "gcal" {
		t.Errorf("Disabled = %v, want [gcal]", got)
	}
}

func write(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
