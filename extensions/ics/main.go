// Command task-sync-ics mirrors iCalendar feeds (URLs or local files) into
// tasks. It is the reference syncer extension: a standalone binary that
// talks to the daemon exclusively through the public API (pkg/syncer), so
// anything it does an external program in any language can do identically.
//
// The daemon's extension host runs it with the extension folder as working
// directory and TASKD_ADDR set, so config.yaml is read relative to cwd. On
// a config error it exits nonzero and lets the supervisor's backoff handle
// the restart.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/serteal/taskd/pkg/syncer"
)

const configPath = "config.yaml"

type config struct {
	Interval  time.Duration
	Calendars []calendar
}

// calendar is one feed from config.yaml; the task source becomes
// "ics:<name>".
type calendar struct {
	Name string `yaml:"name"`
	// URL or Path locates the feed; exactly one must be set.
	URL  string `yaml:"url"`
	Path string `yaml:"path"`
	// Labels applied to every task this calendar creates or updates
	// (UpsertExternalTasksRequest.apply_labels).
	Labels []string `yaml:"labels"`
}

func main() {
	cfg, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "task-sync-ics: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	tc := syncer.Client()
	var wg sync.WaitGroup
	for _, cal := range cfg.Calendars {
		wg.Add(1)
		go func() {
			defer wg.Done()
			syncer.Run(ctx, "ics:"+cal.Name, cfg.Interval, func(ctx context.Context) error {
				return syncCalendar(ctx, tc, cal, time.Now)
			})
		}()
	}
	wg.Wait() // each Run returns only when ctx ends, i.e. on SIGINT/SIGTERM
}

func loadConfig(path string) (*config, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%s not found: copy config.yaml.example to config.yaml next to the binary and edit it", path)
	}
	if err != nil {
		return nil, err
	}
	return parseConfig(b)
}

func parseConfig(b []byte) (*config, error) {
	// interval is a Go duration string ("15m"); yaml.v3 cannot decode that
	// into time.Duration directly, so it lands in a string first.
	var raw struct {
		Interval  string     `yaml:"interval"`
		Calendars []calendar `yaml:"calendars"`
	}
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parsing config.yaml: %w", err)
	}
	cfg := &config{Interval: 15 * time.Minute, Calendars: raw.Calendars}
	if raw.Interval != "" {
		d, err := time.ParseDuration(raw.Interval)
		if err != nil {
			return nil, fmt.Errorf("config.yaml: interval: %w", err)
		}
		if d <= 0 {
			return nil, fmt.Errorf("config.yaml: interval must be positive, got %s", d)
		}
		cfg.Interval = d
	}
	if len(cfg.Calendars) == 0 {
		return nil, fmt.Errorf("config.yaml: no calendars configured")
	}
	seen := make(map[string]bool, len(cfg.Calendars))
	for _, c := range cfg.Calendars {
		if c.Name == "" {
			return nil, fmt.Errorf("config.yaml: calendar name is required")
		}
		if seen[c.Name] {
			return nil, fmt.Errorf("config.yaml: duplicate calendar name %q", c.Name)
		}
		seen[c.Name] = true
		if (c.URL == "") == (c.Path == "") {
			return nil, fmt.Errorf("config.yaml: calendar %q: exactly one of url or path must be set", c.Name)
		}
	}
	return cfg, nil
}
