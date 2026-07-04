package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"todoapp/internal/syncer"
)

// DefaultListen matches pkg/client.DefaultTarget.
const DefaultListen = "127.0.0.1:7517"

// FileConfig is dir/config.yaml. Everything is optional; a missing file
// means all defaults.
type FileConfig struct {
	// TCP listen address, default 127.0.0.1:7517.
	Listen string `yaml:"listen"`
	// Optional unix socket to also serve on (created 0600).
	Socket  string       `yaml:"socket"`
	Syncers []SyncerYAML `yaml:"syncers"`
}

// SyncerYAML is one syncers[] entry.
type SyncerYAML struct {
	Type string `yaml:"type"`
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
	Path string `yaml:"path"`
	// Go duration string, e.g. "15m".
	Interval string   `yaml:"interval"`
	Labels   []string `yaml:"labels"`
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

func (y SyncerYAML) toConfig() (syncer.Config, error) {
	cfg := syncer.Config{Type: y.Type, Name: y.Name, URL: y.URL, Path: y.Path, Labels: y.Labels}
	if y.Interval != "" {
		d, err := time.ParseDuration(y.Interval)
		if err != nil {
			return cfg, fmt.Errorf("syncer %s/%s: bad interval %q: %w", y.Type, y.Name, y.Interval, err)
		}
		cfg.Interval = d
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
