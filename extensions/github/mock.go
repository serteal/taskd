// mock.go fabricates a deterministic set of GitHub issues and pull requests.
// There is no network access and no randomness: mockIssues is a pure function
// of (repos, now), so identical inputs always yield identical tasks. `now` is
// used only to place completed_time a few days back on closed/merged items,
// which keeps the data looking fresh without breaking determinism for a fixed
// clock.
//
// A real syncer replaces this file with GitHub REST calls; the shape of the
// ExternalTasks it produces — external_ref, title, completed_time, and the
// external_data keys below — would be identical, so the web presenter needs no
// changes to switch from mock to live data.

package main

import (
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskpb "todoapp/gen/task"
)

// mockItem is one fabricated issue or PR. repoIdx selects the repository from
// the configured list (wrapping when fewer repos are configured); number is
// globally unique across items, so the resulting external_refs never collide
// regardless of that mapping. closedDays is only meaningful for closed/merged
// items — it places completed_time that many days before now.
type mockItem struct {
	repoIdx    int
	number     int
	kind       string // "issue" | "pr"
	state      string // "open" | "closed" | "merged" | "draft"
	title      string
	author     string
	comments   int
	additions  int // PRs only
	deletions  int // PRs only
	closedDays int
	body       string
}

// mockItems is the fixed dataset: a realistic mix of open/closed issues and
// open/draft/merged PRs. repoIdx 0/1/2 map to the three default repos.
var mockItems = []mockItem{
	{repoIdx: 0, number: 41, kind: "issue", state: "open", author: "serteal", comments: 7,
		title: "Race in watch hub on slow consumer",
		body:  "WatchTasks holds the hub mutex while sending to each subscriber, so one slow consumer stalls every watcher. Send outside the lock or drop with a bounded buffer."},
	{repoIdx: 0, number: 38, kind: "pr", state: "merged", author: "octocat", comments: 3, additions: 212, deletions: 64, closedDays: 4,
		title: "Bump connectrpc to v2"},
	{repoIdx: 0, number: 45, kind: "issue", state: "open", author: "mira-k", comments: 2,
		title: "ListTasks keyset pagination skips ties on identical create_time"},
	{repoIdx: 0, number: 33, kind: "pr", state: "open", author: "ana-dev", comments: 5, additions: 156, deletions: 22,
		title: "Optimistic concurrency via expected_revision on UpdateTask"},
	{repoIdx: 0, number: 29, kind: "issue", state: "closed", author: "serteal", comments: 4, closedDays: 9,
		title: "Nil deref merging external_data during upsert",
		body:  "Reconcile panics when a batch task has no external_data and the stored one does. Guard the merge and add a regression test."},
	{repoIdx: 0, number: 47, kind: "pr", state: "open", author: "ana-dev", comments: 0, additions: 64, deletions: 8,
		title: "Cache ListLabels counts between writes"},
	{repoIdx: 1, number: 112, kind: "issue", state: "open", author: "ana-dev", comments: 9,
		title: "Add keyboard navigation to the detail panel",
		body:  "Arrow keys should move focus between fields, Enter saves, and Esc closes the panel. Today everything is mouse-only."},
	{repoIdx: 1, number: 108, kind: "pr", state: "draft", author: "serteal", comments: 1, additions: 430, deletions: 12,
		title: "WIP: virtualized task list for large inboxes"},
	{repoIdx: 1, number: 101, kind: "pr", state: "merged", author: "octocat", comments: 2, additions: 18, deletions: 6, closedDays: 2,
		title: "Fix chip contrast in the dark theme"},
	{repoIdx: 1, number: 96, kind: "issue", state: "open", author: "mira-k", comments: 3,
		title: "Row pulse animation janky on Firefox"},
	{repoIdx: 1, number: 88, kind: "issue", state: "closed", author: "ana-dev", comments: 6, closedDays: 14,
		title: "Timezone off-by-one in the due-date input"},
	{repoIdx: 1, number: 120, kind: "pr", state: "open", author: "serteal", comments: 8, additions: 302, deletions: 40,
		title: "Presenter registry for extension row + detail rendering"},
	{repoIdx: 1, number: 124, kind: "issue", state: "open", author: "mira-k", comments: 1,
		title: "Detail panel should show raw source data when no presenter matches"},
	{repoIdx: 2, number: 57, kind: "issue", state: "open", author: "devops-sam", comments: 11,
		title: "Terraform drift on staging RDS parameter group",
		body:  "Every apply plans a destroy/create on the parameter group even with no changes. Suspect a provider default that isn't set explicitly in the module."},
	{repoIdx: 2, number: 54, kind: "pr", state: "merged", author: "octocat", comments: 1, additions: 9, deletions: 9, closedDays: 6,
		title: "Pin CI runners to ubuntu-24.04"},
	{repoIdx: 2, number: 49, kind: "issue", state: "open", author: "devops-sam", comments: 4,
		title: "Nightly backup job times out on the metrics volume"},
	{repoIdx: 2, number: 61, kind: "pr", state: "open", author: "mira-k", comments: 2, additions: 140, deletions: 3,
		title: "Add Prometheus alerts for queue depth"},
	{repoIdx: 2, number: 44, kind: "issue", state: "closed", author: "devops-sam", comments: 0, closedDays: 21,
		title: "Secret rotation runbook out of date"},
}

// mockIssues renders the fixed dataset against the configured repos and clock.
// completed_time is set only for closed/merged items; open and draft items are
// left active. due_time is never set — issues and PRs aren't due-dated.
func mockIssues(repos []string, now time.Time) []*taskpb.ExternalTask {
	if len(repos) == 0 {
		repos = defaultRepos
	}
	tasks := make([]*taskpb.ExternalTask, 0, len(mockItems))
	for _, it := range mockItems {
		repo := repos[it.repoIdx%len(repos)]
		seg := "issues"
		if it.kind == "pr" {
			seg = "pull"
		}
		url := fmt.Sprintf("https://github.com/%s/%s/%d", repo, seg, it.number)

		data := map[string]any{
			"repo":     repo,
			"number":   it.number,
			"kind":     it.kind,
			"state":    it.state,
			"author":   it.author,
			"url":      url,
			"comments": it.comments,
		}
		if it.kind == "pr" {
			data["additions"] = it.additions
			data["deletions"] = it.deletions
		}
		if it.body != "" {
			data["body"] = it.body
		}
		// Every value above is a scalar structpb accepts, so this never errors;
		// MustStruct-style construction would be equivalent.
		st, err := structpb.NewStruct(data)
		if err != nil {
			panic(fmt.Sprintf("github mock: external_data for %s: %v", url, err))
		}

		t := &taskpb.ExternalTask{
			ExternalRef:  url,
			Title:        it.title,
			ExternalData: st,
		}
		if it.state == "closed" || it.state == "merged" {
			t.CompletedTime = timestamppb.New(now.Add(-time.Duration(it.closedDays) * 24 * time.Hour))
		}
		tasks = append(tasks, t)
	}
	return tasks
}
