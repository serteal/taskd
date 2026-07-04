// Command task-sync-gcal is a MOCK Google Calendar syncer: it fabricates a
// realistic week of calendar events and mirrors them into tasks. It exists to
// exercise the calendar-event presenter and the Calendar week view without a
// real Google account or API key.
//
// It is structured exactly like the ics reference syncer — a standalone
// binary that talks to the daemon only through the public API (pkg/syncer) —
// but instead of fetching a feed it calls mockEvents(now). Swapping mock.go
// for a real Google Calendar API (or per-calendar ICS URL) fetch is all it
// would take to make this real; the web half would not change at all.
//
// The daemon's extension host runs it with the extension folder as the
// working directory and TASKD_ADDR set, so config.yaml is read relative to
// cwd. Unlike ics, config.yaml is OPTIONAL here: mock data needs no real
// configuration, so a missing config is not fatal — the syncer logs an info
// line and runs with a single default account.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"gopkg.in/yaml.v3"

	taskpb "todoapp/gen/task"
	"todoapp/gen/task/taskconnect"
	"todoapp/pkg/syncer"
)

const configPath = "config.yaml"

type config struct {
	Interval time.Duration
	Accounts []account
}

// account is one mock calendar from config.yaml; the task source becomes
// "gcal:<name>".
type account struct {
	Name string `yaml:"name"`
	// Labels applied to every task this account creates or updates
	// (UpsertExternalTasksRequest.apply_labels).
	Labels []string `yaml:"labels"`
}

// defaultConfig is what runs when config.yaml is absent or lists no accounts:
// one account named "personal" labelled "calendar", synced every 15m.
func defaultConfig() *config {
	return &config{
		Interval: 15 * time.Minute,
		Accounts: []account{{Name: "personal", Labels: []string{"calendar"}}},
	}
}

func main() {
	cfg, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "task-sync-gcal: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	tc := syncer.Client()
	var wg sync.WaitGroup
	for _, acct := range cfg.Accounts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			syncer.Run(ctx, "gcal:"+acct.Name, cfg.Interval, func(ctx context.Context) error {
				return syncAccount(ctx, tc, acct, time.Now)
			})
		}()
	}
	wg.Wait() // each Run returns only when ctx ends, i.e. on SIGINT/SIGTERM
}

// syncAccount fabricates the account's current week and upserts it as the
// account's full snapshot. Per the API's ownership rules the "source" (here,
// the mock generator) owns title, due_time, completed_time, and
// external_data; labels and notes stay with the user.
func syncAccount(ctx context.Context, tc taskconnect.TaskServiceClient, acct account, now func() time.Time) error {
	source := "gcal:" + acct.Name
	if _, err := tc.UpsertExternalTasks(ctx, connect.NewRequest(&taskpb.UpsertExternalTasksRequest{
		Source:       source,
		Tasks:        mockEvents(now()),
		ApplyLabels:  acct.Labels,
		FullSnapshot: true,
	})); err != nil {
		return fmt.Errorf("%s: upsert: %w", source, err)
	}
	return nil
}

func loadConfig(path string) (*config, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		log.Printf("gcal: %s not found; using defaults (account %q, labels [calendar]) — mock data needs no config", path, "personal")
		return defaultConfig(), nil
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
		Interval string    `yaml:"interval"`
		Accounts []account `yaml:"accounts"`
	}
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parsing config.yaml: %w", err)
	}
	cfg := &config{Interval: 15 * time.Minute, Accounts: raw.Accounts}
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
	if len(cfg.Accounts) == 0 {
		cfg.Accounts = defaultConfig().Accounts
	}
	seen := make(map[string]bool, len(cfg.Accounts))
	for _, a := range cfg.Accounts {
		if a.Name == "" {
			return nil, fmt.Errorf("config.yaml: account name is required")
		}
		if seen[a.Name] {
			return nil, fmt.Errorf("config.yaml: duplicate account name %q", a.Name)
		}
		seen[a.Name] = true
	}
	return cfg, nil
}
