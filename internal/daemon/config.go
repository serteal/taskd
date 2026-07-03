package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"todoapp/internal/plugin"
)

// FileConfig is $TASKD_DIR/config.yaml — the phase-2 home of connector
// instances. It moves behind ConfigService when rules land; the file format
// stays as import/export.
//
//	retention: 720h            # optional, event-log retention
//	instances:
//	  - name: cal@personal     # instance name = link namespace
//	    plugin: ics            # bare name → $TASKD_DIR/plugins/<name>, or an absolute path
//	    poll: 5m               # optional; else the plugin's hint, floor 30s
//	    config:                # opaque, passed to the plugin's Configure
//	      url: file:///Users/me/personal.ics
//	      horizon_days: 60
type FileConfig struct {
	Retention time.Duration    `yaml:"retention"`
	Instances []InstanceConfig `yaml:"instances"`
}

type InstanceConfig struct {
	Name   string         `yaml:"name"`
	Plugin string         `yaml:"plugin"`
	Poll   time.Duration  `yaml:"poll"`
	Config map[string]any `yaml:"config"`
}

func ConfigPath(dir string) string { return filepath.Join(dir, "config.yaml") }

// loadFileConfig reads the config file; a missing file is an empty config.
func loadFileConfig(dir string) (FileConfig, error) {
	var fc FileConfig
	raw, err := os.ReadFile(ConfigPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return fc, nil
	}
	if err != nil {
		return fc, fmt.Errorf("config: %w", err)
	}
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true) // typos in config must fail loudly, not silently no-op
	if err := dec.Decode(&fc); err != nil {
		return fc, fmt.Errorf("config %s: %w", ConfigPath(dir), err)
	}
	seen := map[string]bool{}
	for i, inst := range fc.Instances {
		if inst.Name == "" || inst.Plugin == "" {
			return fc, fmt.Errorf("config: instance %d needs both name and plugin", i)
		}
		if seen[inst.Name] {
			return fc, fmt.Errorf("config: duplicate instance name %q", inst.Name)
		}
		seen[inst.Name] = true
	}
	return fc, nil
}

// resolveInstance turns an InstanceConfig into the host's Instance,
// resolving bare plugin names against $dir/plugins.
func resolveInstance(dir string, ic InstanceConfig) (plugin.Instance, error) {
	bin := ic.Plugin
	if !filepath.IsAbs(bin) {
		bin = filepath.Join(dir, "plugins", ic.Plugin)
	}
	if _, err := os.Stat(bin); err != nil {
		return plugin.Instance{}, fmt.Errorf("instance %q: plugin binary %s: %w", ic.Name, bin, err)
	}
	return plugin.Instance{
		Name:   ic.Name,
		Binary: bin,
		Config: ic.Config,
		Poll:   ic.Poll,
	}, nil
}
