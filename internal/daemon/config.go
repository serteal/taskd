package daemon

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DefaultListen matches pkg/client.DefaultTarget.
const DefaultListen = "127.0.0.1:8888"

// FileConfig is dir/config.yaml. Everything is optional; a missing file
// means all defaults. Integrations are not configured here — they are
// extensions (folders under dir/extensions, see internal/extension) — but
// which of those extensions are turned off IS operational state, so it
// lives here (written back by the admin API, not hand-edited in practice).
type FileConfig struct {
	// TCP listen address, default 127.0.0.1:8888.
	Listen string `yaml:"listen,omitempty"`
	// Optional unix socket to also serve on (created 0600).
	Socket string `yaml:"socket,omitempty"`
	// Open the web app in a browser on startup: true always opens, false
	// suppresses even the first-run auto-open; unset opens on first run only.
	Open *bool `yaml:"open,omitempty"`
	// Extension host settings.
	Extensions ExtensionsConfig `yaml:"extensions,omitempty"`
}

// ExtensionsConfig is the daemon's view of the installed extensions — not
// their config (each extension owns that in its own folder), only which the
// daemon supervises and serves.
type ExtensionsConfig struct {
	// Names of extensions to leave installed but dormant: not supervised and
	// not served. Toggled through the admin API's SetExtensionEnabled.
	Disabled []string `yaml:"disabled,omitempty"`
}

func loadFileConfig(dir string) (FileConfig, error) {
	cfg := FileConfig{Listen: DefaultListen}
	b, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing config.yaml: %w", err)
	}
	if cfg.Listen == "" {
		cfg.Listen = DefaultListen
	}
	return cfg, nil
}

// saveFileConfig persists cfg.Extensions to dir/config.yaml, preserving
// whatever else is already on disk. It deliberately re-reads the file for
// listen/socket rather than serializing a running FileConfig: a running
// config carries a defaulted or --listen-overridden address that must never
// leak into the file and clobber what the user wrote (or didn't). Only the
// extensions section is authoritative here, so only it is overwritten.
func saveFileConfig(dir string, cfg FileConfig) error {
	var onDisk FileConfig
	b, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		if err := yaml.Unmarshal(b, &onDisk); err != nil {
			return fmt.Errorf("parsing config.yaml: %w", err)
		}
	}
	onDisk.Extensions = cfg.Extensions
	out, err := yaml.Marshal(&onDisk)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.yaml"), out, 0o600)
}

// Dir resolves the data directory: TASKD_DIR env var, else ~/.taskd.
func Dir() (string, error) {
	if v := os.Getenv("TASKD_DIR"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".taskd"), nil
}
