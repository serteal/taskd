package daemon

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DefaultListen matches pkg/client.DefaultTarget.
const DefaultListen = "127.0.0.1:7517"

// FileConfig is dir/config.yaml. Everything is optional; a missing file
// means all defaults. Integrations are not configured here — they are
// extensions (folders under dir/extensions, see internal/extension).
type FileConfig struct {
	// TCP listen address, default 127.0.0.1:7517.
	Listen string `yaml:"listen"`
	// Optional unix socket to also serve on (created 0600).
	Socket string `yaml:"socket"`
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
