package main

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
)

var fixedNow = time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)

func TestMockIssuesDeterministic(t *testing.T) {
	a := mockIssues(defaultRepos, fixedNow)
	b := mockIssues(defaultRepos, fixedNow)
	if len(a) != len(b) {
		t.Fatalf("length differs between runs: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if !proto.Equal(a[i], b[i]) {
			t.Errorf("item %d differs between identical runs:\n a=%v\n b=%v", i, a[i], b[i])
		}
	}
}

func TestMockIssuesCount(t *testing.T) {
	got := mockIssues(defaultRepos, fixedNow)
	if len(got) < 15 || len(got) > 20 {
		t.Fatalf("want 15-20 fabricated items, got %d", len(got))
	}
}

func TestMockIssuesUniqueRefs(t *testing.T) {
	seen := map[string]bool{}
	for _, task := range mockIssues(defaultRepos, fixedNow) {
		ref := task.GetExternalRef()
		if ref == "" {
			t.Error("empty external_ref")
			continue
		}
		if !strings.HasPrefix(ref, "https://github.com/") {
			t.Errorf("external_ref is not a github URL: %q", ref)
		}
		if seen[ref] {
			t.Errorf("duplicate external_ref %q", ref)
		}
		seen[ref] = true
	}
}

func TestMockIssuesCompletionTracksState(t *testing.T) {
	for _, task := range mockIssues(defaultRepos, fixedNow) {
		d := task.GetExternalData().AsMap()
		state, _ := d["state"].(string)
		hasCompleted := task.GetCompletedTime() != nil
		wantCompleted := state == "closed" || state == "merged"
		if hasCompleted != wantCompleted {
			t.Errorf("%s: state %q has completed_time=%v, want %v",
				task.GetExternalRef(), state, hasCompleted, wantCompleted)
		}
		if task.GetDueTime() != nil {
			t.Errorf("%s: due_time should never be set on issues/PRs", task.GetExternalRef())
		}
	}
}

func TestMockIssuesExternalData(t *testing.T) {
	for _, task := range mockIssues(defaultRepos, fixedNow) {
		ref := task.GetExternalRef()
		d := task.GetExternalData().AsMap()
		for _, k := range []string{"repo", "number", "kind", "state", "author", "url", "comments"} {
			if _, ok := d[k]; !ok {
				t.Errorf("%s: missing external_data key %q", ref, k)
			}
		}
		// number and url must be well-formed and consistent with the ref.
		if _, ok := d["number"].(float64); !ok {
			t.Errorf("%s: number is not numeric: %T", ref, d["number"])
		}
		if d["url"] != ref {
			t.Errorf("%s: url %v does not match external_ref", ref, d["url"])
		}

		kind, _ := d["kind"].(string)
		switch kind {
		case "issue":
			if _, ok := d["additions"]; ok {
				t.Errorf("%s: issue must not carry additions", ref)
			}
		case "pr":
			if _, ok := d["additions"].(float64); !ok {
				t.Errorf("%s: PR missing numeric additions", ref)
			}
			if _, ok := d["deletions"].(float64); !ok {
				t.Errorf("%s: PR missing numeric deletions", ref)
			}
		default:
			t.Errorf("%s: unexpected kind %q", ref, kind)
		}
	}
}

func TestMockIssuesHasBodies(t *testing.T) {
	var withBody int
	for _, task := range mockIssues(defaultRepos, fixedNow) {
		if _, ok := task.GetExternalData().AsMap()["body"]; ok {
			withBody++
		}
	}
	if withBody < 2 {
		t.Errorf("want at least a couple of items with a body, got %d", withBody)
	}
}

func TestMockIssuesRespectsRepos(t *testing.T) {
	// A single configured repo must funnel every item into it while keeping
	// refs unique (guaranteed by globally unique issue numbers).
	repos := []string{"solo/repo"}
	seen := map[string]bool{}
	for _, task := range mockIssues(repos, fixedNow) {
		d := task.GetExternalData().AsMap()
		if d["repo"] != "solo/repo" {
			t.Errorf("%s: repo %v, want solo/repo", task.GetExternalRef(), d["repo"])
		}
		if seen[task.GetExternalRef()] {
			t.Errorf("duplicate ref collapsing to one repo: %s", task.GetExternalRef())
		}
		seen[task.GetExternalRef()] = true
	}
}

func TestMockIssuesEmptyReposFallsBack(t *testing.T) {
	got := mockIssues(nil, fixedNow)
	if len(got) == 0 {
		t.Fatal("nil repos should fall back to defaults, got no tasks")
	}
	for _, task := range got {
		repo, _ := task.GetExternalData().AsMap()["repo"].(string)
		found := false
		for _, r := range defaultRepos {
			if r == repo {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s: repo %q not among defaults %v", task.GetExternalRef(), repo, defaultRepos)
		}
	}
}
