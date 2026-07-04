// Command task-sync-github is a MOCK GitHub sync extension. It fabricates a
// deterministic set of issues and pull requests across a few repositories and
// upserts them into taskd as tasks. There is no network access, no GitHub API,
// and no credentials: it exists to exercise taskd's presenter/detail extension
// surface with realistic bug-tracker data.
//
// A real version swaps mock.go for the GitHub REST API (listing issues and PRs
// per repo) and could add close-on-complete write-back via syncer.WatchChanges
// (task completed locally -> close the remote issue). Everything else — config
// loading, the single "github" source, and the UpsertExternalTasks
// full-snapshot call — stays exactly the same.
//
// Like every syncer it talks to the daemon only through the public API
// (pkg/syncer), with no privileged access. The daemon runs it with the
// extension folder as working directory and TASKD_ADDR set, so config.yaml is
// read relative to cwd.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"gopkg.in/yaml.v3"

	taskpb "todoapp/gen/task"
	"todoapp/pkg/syncer"
)

const (
	configPath = "config.yaml"
	source     = "github"
)

// defaultRepos are the fabricated repositories that mock issues are spread
// across when config.yaml is absent or lists none.
var defaultRepos = []string{"taskd/core", "taskd/web", "acme/infra"}

type config struct {
	Interval time.Duration
	Repos    []string
}

func main() {
	cfg := loadConfig(configPath)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	tc := syncer.Client()
	// One source, one snapshot per tick. apply_labels adds the shared "bug"
	// label to every item (additive — it never removes the user's labels);
	// per-item repo/kind/state live in external_data and drive the presenter.
	syncer.Run(ctx, source, cfg.Interval, func(ctx context.Context) error {
		tasks := mockIssues(cfg.Repos, time.Now())
		_, err := tc.UpsertExternalTasks(ctx, connect.NewRequest(&taskpb.UpsertExternalTasksRequest{
			Source:       source,
			Tasks:        tasks,
			ApplyLabels:  []string{"bug"},
			FullSnapshot: true,
		}))
		return err
	})
}

// loadConfig reads config.yaml if it exists. Unlike the ics reference syncer a
// missing config is NOT fatal here: the mock has sensible defaults, so we log
// and carry on. A malformed file or a bad interval is likewise logged, and
// defaults fill in for the affected field rather than crashing the syncer.
func loadConfig(path string) config {
	cfg := config{Interval: 15 * time.Minute, Repos: defaultRepos}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		log.Printf("%s: no %s next to the binary; using defaults (interval %s, repos %v)",
			source, path, cfg.Interval, cfg.Repos)
		return cfg
	}
	if err != nil {
		log.Printf("%s: reading %s: %v; using defaults", source, path, err)
		return cfg
	}
	// interval is a Go duration string ("15m"); yaml.v3 cannot decode that into
	// time.Duration directly, so it lands in a string first.
	var raw struct {
		Interval string   `yaml:"interval"`
		Repos    []string `yaml:"repos"`
	}
	if err := yaml.Unmarshal(b, &raw); err != nil {
		log.Printf("%s: parsing %s: %v; using defaults", source, path, err)
		return cfg
	}
	if raw.Interval != "" {
		if d, err := time.ParseDuration(raw.Interval); err != nil || d <= 0 {
			log.Printf("%s: %s: invalid interval %q; using %s", source, path, raw.Interval, cfg.Interval)
		} else {
			cfg.Interval = d
		}
	}
	if len(raw.Repos) > 0 {
		cfg.Repos = raw.Repos
	}
	return cfg
}
