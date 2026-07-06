package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	taskpb "github.com/serteal/taskd/gen/task"
	"github.com/serteal/taskd/gen/task/taskconnect"
	"github.com/serteal/taskd/internal/server"
	"github.com/serteal/taskd/internal/store"
)

// startServer runs the real store+server stack over httptest — the same
// pattern internal/server uses in its own tests. The direct client is for
// out-of-band setup and verification.
func startServer(t *testing.T) (addr string, tc taskconnect.TaskServiceClient) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "e2e.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	srv := server.New(st)
	path, handler := srv.Handler()
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts.URL, taskconnect.NewTaskServiceClient(ts.Client(), ts.URL)
}

// execCLI runs one CLI invocation against addr, capturing stdout and stderr.
func execCLI(addr, stdin string, args ...string) (stdout, stderr string, err error) {
	var out, errOut bytes.Buffer
	cmd := newRootCmd(&out)
	cmd.SetArgs(append([]string{"--addr", addr}, args...))
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetErr(&errOut)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// runCLI is execCLI that fails the test on error and returns stdout.
func runCLI(t *testing.T, addr, stdin string, args ...string) string {
	t.Helper()
	out, errOut, err := execCLI(addr, stdin, args...)
	if err != nil {
		t.Fatalf("task %s: %v (stdout %q, stderr %q)", strings.Join(args, " "), err, out, errOut)
	}
	return out
}

func mustContain(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("output missing %q:\n%s", w, got)
		}
	}
}

func mustNotContain(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if strings.Contains(got, w) {
			t.Errorf("output unexpectedly has %q:\n%s", w, got)
		}
	}
}

func TestE2ELifecycle(t *testing.T) {
	addr, _ := startServer(t)

	out := runCLI(t, addr, "", "add", "Buy", "milk", "-l", "home", "-l", "p1", "--due", "tomorrow")
	fields := strings.Fields(out)
	if len(fields) < 2 || len(fields[0]) != 8 {
		t.Fatalf("add output = %q, want '<8-char id> Buy milk'", out)
	}
	short := fields[0]
	if !strings.HasSuffix(strings.TrimSpace(out), "Buy milk") {
		t.Fatalf("add output = %q, want title 'Buy milk'", out)
	}

	ls := runCLI(t, addr, "", "ls")
	mustContain(t, ls, "Buy milk", "#home", "#p1", "tomorrow")

	// Complete: gone from the default listing, marked in --completed/--all.
	mustContain(t, runCLI(t, addr, "", "done", short), "done", "Buy milk")
	mustNotContain(t, runCLI(t, addr, "", "ls"), "Buy milk")
	completed := runCLI(t, addr, "", "ls", "--completed")
	mustContain(t, completed, "Buy milk", "✓")
	mustContain(t, runCLI(t, addr, "", "ls", "--all"), "Buy milk")

	mustContain(t, runCLI(t, addr, "", "undone", short), "reopened", "Buy milk")
	mustContain(t, runCLI(t, addr, "", "ls"), "Buy milk")

	// One masked edit touching labels, notes, title, and due.
	runCLI(t, addr, "", "edit", short, "--add-label", "urgent", "--remove-label", "home", "--notes", "the oat kind")
	show := runCLI(t, addr, "", "show", short)
	mustContain(t, show, "p1, urgent", "the oat kind")
	mustNotContain(t, show, "home")

	runCLI(t, addr, "", "edit", short, "--title", "Buy oat milk", "--clear-due")
	show = runCLI(t, addr, "", "show", short)
	mustContain(t, show, "Buy oat milk", "revision:")
	mustNotContain(t, show, "due:")

	var decoded map[string]any
	if err := json.Unmarshal([]byte(runCLI(t, addr, "", "show", "--json", short)), &decoded); err != nil {
		t.Fatalf("show --json is not JSON: %v", err)
	}
	if decoded["title"] != "Buy oat milk" {
		t.Errorf("show --json title = %v", decoded["title"])
	}

	labels := runCLI(t, addr, "", "labels")
	counts := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(labels), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 {
			counts[f[0]] = f[1]
		}
	}
	if counts["p1"] != "1" || counts["urgent"] != "1" {
		t.Errorf("labels counts = %v, want p1=1 urgent=1", counts)
	}

	// rm asks first; anything but yes skips.
	mustContain(t, runCLI(t, addr, "n\n", "rm", short), "delete Buy oat milk? [y/N]", "skipped")
	mustContain(t, runCLI(t, addr, "", "ls"), "Buy oat milk")

	mustContain(t, runCLI(t, addr, "", "rm", "-f", short), "deleted")
	if got := strings.TrimSpace(runCLI(t, addr, "", "ls")); got != "" {
		t.Errorf("ls after rm = %q, want empty", got)
	}

	// The prompt's yes path deletes too.
	out = runCLI(t, addr, "", "add", "temp task")
	mustContain(t, runCLI(t, addr, "y\n", "rm", strings.Fields(out)[0]), "deleted")
	if got := strings.TrimSpace(runCLI(t, addr, "", "ls")); got != "" {
		t.Errorf("ls after prompted rm = %q, want empty", got)
	}
}

func TestE2ELsFilters(t *testing.T) {
	addr, tc := startServer(t)
	ctx := context.Background()

	runCLI(t, addr, "", "add", "alpha task", "-l", "work", "--due", "2030-01-02")
	runCLI(t, addr, "", "add", "beta task", "-l", "home")
	if _, err := tc.UpsertExternalTasks(ctx, connect.NewRequest(&taskpb.UpsertExternalTasksRequest{
		Source: "gh",
		Tasks:  []*taskpb.ExternalTask{{ExternalRef: "pr-1", Title: "review pr"}},
	})); err != nil {
		t.Fatalf("upsert external: %v", err)
	}

	ls := runCLI(t, addr, "", "ls")
	mustContain(t, ls, "alpha task", "beta task", "review pr", "[gh]")
	// Default order is due asc; the server sorts no-due tasks last.
	if strings.Index(ls, "alpha task") > strings.Index(ls, "beta task") {
		t.Errorf("due-asc default should list alpha (has due) first:\n%s", ls)
	}

	byLabel := runCLI(t, addr, "", "ls", "-l", "work")
	mustContain(t, byLabel, "alpha task")
	mustNotContain(t, byLabel, "beta task", "review pr")

	byText := runCLI(t, addr, "", "ls", "beta")
	mustContain(t, byText, "beta task")
	mustNotContain(t, byText, "alpha task", "review pr")

	bySource := runCLI(t, addr, "", "ls", "--source", "gh")
	mustContain(t, bySource, "review pr", "[gh]")
	mustNotContain(t, bySource, "alpha task", "beta task")

	hasDue := runCLI(t, addr, "", "ls", "--has-due")
	mustContain(t, hasDue, "alpha task")
	mustNotContain(t, hasDue, "beta task", "review pr")

	noDue := runCLI(t, addr, "", "ls", "--no-due")
	mustContain(t, noDue, "beta task", "review pr")
	mustNotContain(t, noDue, "alpha task")

	before := runCLI(t, addr, "", "ls", "--due-before", "2031-01-01")
	mustContain(t, before, "alpha task")
	mustNotContain(t, before, "beta task", "review pr")

	if got := strings.TrimSpace(runCLI(t, addr, "", "ls", "--due-after", "2031-01-01")); got != "" {
		t.Errorf("ls --due-after 2031 = %q, want empty", got)
	}

	titleDesc := runCLI(t, addr, "", "ls", "--order", "title:desc")
	if !(strings.Index(titleDesc, "review pr") < strings.Index(titleDesc, "beta task") &&
		strings.Index(titleDesc, "beta task") < strings.Index(titleDesc, "alpha task")) {
		t.Errorf("title:desc order wrong:\n%s", titleDesc)
	}

	limited := runCLI(t, addr, "", "ls", "--order", "title:asc", "--limit", "1")
	mustContain(t, limited, "alpha task")
	mustNotContain(t, limited, "beta task", "review pr")

	var decoded []map[string]any
	if err := json.Unmarshal([]byte(runCLI(t, addr, "", "ls", "--all", "--json")), &decoded); err != nil {
		t.Fatalf("ls --json is not a JSON array: %v", err)
	}
	if len(decoded) != 3 {
		t.Errorf("ls --all --json = %d tasks, want 3", len(decoded))
	}

	if _, _, err := execCLI(addr, "", "ls", "--order", "bogus"); err == nil || !strings.Contains(err.Error(), "invalid order") {
		t.Errorf("ls --order bogus err = %v, want invalid-order error", err)
	}
}

// TestE2EOrderCompleted: --order completed is accepted and sorts by completion
// time, with never-completed tasks last regardless of direction.
func TestE2EOrderCompleted(t *testing.T) {
	addr, tc := startServer(t)
	ctx := context.Background()

	runCLI(t, addr, "", "add", "first")
	runCLI(t, addr, "", "add", "second")
	// The two ids can share an 8-char prefix (same millisecond), so complete
	// "second" by its full, unambiguous id.
	list, err := tc.ListTasks(ctx, connect.NewRequest(&taskpb.ListTasksRequest{
		Filter: &taskpb.TaskFilter{Text: "second"},
	}))
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(list.Msg.GetTasks()) != 1 {
		t.Fatalf("want exactly one task titled second, got %d", len(list.Msg.GetTasks()))
	}
	runCLI(t, addr, "", "done", list.Msg.GetTasks()[0].GetId())

	// completed:desc → the completed task before the never-completed one.
	ls := runCLI(t, addr, "", "ls", "--all", "--order", "completed:desc")
	mustContain(t, ls, "first", "second")
	if strings.Index(ls, "second") > strings.Index(ls, "first") {
		t.Errorf("completed:desc should list the completed task first:\n%s", ls)
	}
}

// TestE2ELsCompletedDefaultsToCompletedDesc: `task ls --completed` with no
// explicit --order defaults to completed-desc (newest completion first),
// matching the web archive, rather than the active list's due-asc default.
func TestE2ELsCompletedDefaultsToCompletedDesc(t *testing.T) {
	addr, tc := startServer(t)
	ctx := context.Background()

	runCLI(t, addr, "", "add", "early")
	runCLI(t, addr, "", "add", "late")

	completeByTitle := func(title string) {
		list, err := tc.ListTasks(ctx, connect.NewRequest(&taskpb.ListTasksRequest{
			Filter: &taskpb.TaskFilter{Text: title},
		}))
		if err != nil {
			t.Fatalf("ListTasks %s: %v", title, err)
		}
		if len(list.Msg.GetTasks()) != 1 {
			t.Fatalf("want one task titled %q, got %d", title, len(list.Msg.GetTasks()))
		}
		runCLI(t, addr, "", "done", list.Msg.GetTasks()[0].GetId())
	}
	// Complete "early" first, then "late", so "late" has the newer completion.
	completeByTitle("early")
	completeByTitle("late")

	ls := runCLI(t, addr, "", "ls", "--completed")
	mustContain(t, ls, "early", "late")
	if strings.Index(ls, "late") > strings.Index(ls, "early") {
		t.Errorf("ls --completed should default to completed-desc (late first):\n%s", ls)
	}

	// An explicit --order still wins over the archive default.
	byTitle := runCLI(t, addr, "", "ls", "--completed", "--order", "title:asc")
	if strings.Index(byTitle, "early") > strings.Index(byTitle, "late") {
		t.Errorf("explicit --order title:asc should list early first:\n%s", byTitle)
	}
}

func TestE2EResolvePrefixes(t *testing.T) {
	addr, tc := startServer(t)
	ctx := context.Background()

	mk := func(title string) string {
		res, err := tc.CreateTask(ctx, connect.NewRequest(&taskpb.CreateTaskRequest{Title: title}))
		if err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		return res.Msg.GetTask().GetId()
	}
	id1, id2 := mk("first"), mk("second")

	// Back-to-back ULIDs share their timestamp prefix, so the common prefix
	// is ambiguous while one extra character is unique.
	lcp := 0
	for lcp < len(id1) && id1[lcp] == id2[lcp] {
		lcp++
	}
	if lcp == 0 {
		t.Skipf("ids %s and %s share no prefix", id1, id2)
	}
	_, _, err := execCLI(addr, "", "show", id1[:lcp])
	if err == nil || !strings.Contains(err.Error(), "matches 2 tasks") {
		t.Errorf("ambiguous prefix err = %v, want candidate listing", err)
	}
	mustContain(t, runCLI(t, addr, "", "show", id1[:lcp+1]), "first")
	mustContain(t, runCLI(t, addr, "", "show", id2), "second")
	mustContain(t, runCLI(t, addr, "", "show", strings.ToLower(id1[:lcp+1])), "first")

	_, _, err = execCLI(addr, "", "show", "no-such-prefix")
	if err == nil || !strings.Contains(err.Error(), "no task with id prefix") {
		t.Errorf("missing prefix err = %v, want no-task error", err)
	}
}

func TestE2EImportExport(t *testing.T) {
	addr, tc := startServer(t)
	ctx := context.Background()

	fixture := strings.Join([]string{
		"x 2026-07-01 Pay rent +home @phone",
		"(A) Ship report due:2026-07-10 +work",
		"(B) Water plants @home label:garden",
		"Call mom @phone",
		"",
		"(D) Low priority thing",
		"Buy espresso beans note:unknown",
	}, "\n") + "\n"
	file := filepath.Join(t.TempDir(), "todo.txt")
	if err := os.WriteFile(file, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := execCLI(addr, "", "import", "todotxt", file)
	if err != nil {
		t.Fatalf("import: %v (stderr %q)", err, errOut)
	}
	if !strings.Contains(out, "imported 6 tasks") {
		t.Errorf("import output = %q, want 6 tasks", out)
	}
	mustContain(t, errOut, "(D)", "note:unknown")

	// Verify the mapping through the API, not the CLI.
	res, err := tc.ListTasks(ctx, connect.NewRequest(&taskpb.ListTasksRequest{PageSize: 100}))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byTitle := map[string]*taskpb.Task{}
	var titles []string
	for _, tk := range res.Msg.GetTasks() {
		byTitle[tk.GetTitle()] = tk
		titles = append(titles, tk.GetTitle())
	}
	if len(byTitle) != 6 {
		t.Fatalf("imported titles = %v, want 6", titles)
	}

	rent := byTitle["Pay rent"]
	if rent == nil {
		t.Fatal("Pay rent not imported")
	}
	if want := time.Date(2026, time.July, 1, 12, 0, 0, 0, time.Local); !rent.GetCompletedTime().AsTime().Equal(want) {
		t.Errorf("Pay rent completed at %v, want %v", rent.GetCompletedTime().AsTime(), want)
	}
	if want := []string{"context:phone", "project:home"}; !slices.Equal(rent.GetLabels(), want) {
		t.Errorf("Pay rent labels = %v, want %v", rent.GetLabels(), want)
	}

	ship := byTitle["Ship report"]
	if want := []string{"p1", "project:work"}; ship == nil || !slices.Equal(ship.GetLabels(), want) {
		t.Fatalf("Ship report labels = %v, want %v", ship.GetLabels(), want)
	}
	if want := time.Date(2026, time.July, 10, 23, 59, 59, 0, time.Local); !ship.GetDueTime().AsTime().Equal(want) {
		t.Errorf("Ship report due %v, want %v", ship.GetDueTime().AsTime(), want)
	}

	water := byTitle["Water plants"]
	if want := []string{"context:home", "garden", "p2"}; water == nil || !slices.Equal(water.GetLabels(), want) {
		t.Errorf("Water plants labels = %v, want %v", water.GetLabels(), want)
	}
	if lp := byTitle["Low priority thing"]; lp == nil || len(lp.GetLabels()) != 0 {
		t.Errorf("Low priority thing = %v, want no labels", lp)
	}
	if beans := byTitle["Buy espresso beans"]; beans == nil {
		t.Error("note:unknown tag was not stripped from title")
	}

	// Export round-trips: same lines (order-insensitively), completed last,
	// and every line is a fixed point of parse→format.
	exported := runCLI(t, addr, "", "export", "todotxt")
	lines := strings.Split(strings.TrimSpace(exported), "\n")
	wantLines := []string{
		"(A) Ship report +work due:2026-07-10",
		"(B) Water plants @home label:garden",
		"Call mom @phone",
		"Low priority thing",
		"Buy espresso beans",
		"x 2026-07-01 Pay rent +home @phone",
	}
	if lines[len(lines)-1] != "x 2026-07-01 Pay rent +home @phone" {
		t.Errorf("completed task is not last:\n%s", exported)
	}
	gotSorted, wantSorted := slices.Clone(lines), slices.Clone(wantLines)
	slices.Sort(gotSorted)
	slices.Sort(wantSorted)
	if !slices.Equal(gotSorted, wantSorted) {
		t.Errorf("exported lines = %q, want %q", gotSorted, wantSorted)
	}
	for _, line := range lines {
		tl, ok := parseTodoLine(line, io.Discard)
		if !ok {
			t.Errorf("exported blank line")
			continue
		}
		if got := formatTodoLine(tl); got != line {
			t.Errorf("exported line %q re-formats as %q", line, got)
		}
	}

	// Exporting to a file writes the same bytes as stdout.
	outFile := filepath.Join(t.TempDir(), "export.txt")
	runCLI(t, addr, "", "export", "todotxt", outFile)
	b, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != exported {
		t.Errorf("file export differs from stdout export:\n%q\nvs\n%q", b, exported)
	}
}

func TestE2ERecurrenceAndSubtasks(t *testing.T) {
	addr, tc := startServer(t)
	ctx := context.Background()

	// fullID resolves a title to its full, unambiguous id (roll-forward archives
	// share an 8-char ULID prefix with the live task).
	fullID := func(title string, completed *bool) string {
		t.Helper()
		res, err := tc.ListTasks(ctx, connect.NewRequest(&taskpb.ListTasksRequest{
			Filter: &taskpb.TaskFilter{Text: title, Completed: completed},
		}))
		if err != nil || len(res.Msg.GetTasks()) != 1 {
			t.Fatalf("fullID(%q): %v tasks, err=%v", title, len(res.Msg.GetTasks()), err)
		}
		return res.Msg.GetTasks()[0].GetId()
	}

	// add --every: a recurring task, due in the past so `done` rolls it forward.
	runCLI(t, addr, "", "add", "standup", "--every", "weekday", "--due", "2020-01-06")
	id := fullID("standup", boolPtr(false))

	// show renders both the canonical rule and a humanized form.
	mustContain(t, runCLI(t, addr, "", "show", id),
		"recurrence:", "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR", "every weekday")

	// done on a recurring task surfaces the roll-forward.
	mustContain(t, runCLI(t, addr, "", "done", id), "standup", "occurrence archived", "next due")

	// The task remains active (rolled forward), and a completed archive exists.
	mustContain(t, runCLI(t, addr, "", "ls"), "standup")
	mustContain(t, runCLI(t, addr, "", "ls", "--completed"), "standup", "✓")

	// edit --clear-every stops the recurrence.
	runCLI(t, addr, "", "edit", id, "--clear-every")
	mustNotContain(t, runCLI(t, addr, "", "show", id), "recurrence:")

	// add --parent + ls --tree: subtasks nest under their parent.
	res, err := tc.CreateTask(ctx, connect.NewRequest(&taskpb.CreateTaskRequest{Title: "project"}))
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	parentID := res.Msg.GetTask().GetId()
	runCLI(t, addr, "", "add", "subtask one", "--parent", parentID)
	runCLI(t, addr, "", "add", "subtask two", "--parent", parentID)

	// show on the parent lists its children.
	mustContain(t, runCLI(t, addr, "", "show", parentID), "subtasks:", "subtask one", "subtask two")

	tree := runCLI(t, addr, "", "ls", "--tree")
	flat := runCLI(t, addr, "", "ls")
	mustContain(t, tree, "project", "subtask one", "subtask two")
	// The parent renders before its children, and the children are indented
	// relative to the flat listing.
	if strings.Index(tree, "project") > strings.Index(tree, "subtask one") {
		t.Errorf("--tree should render the parent before its child:\n%s", tree)
	}
	if titleCol(tree, "subtask one") <= titleCol(flat, "subtask one") {
		t.Errorf("--tree should indent children (tree col %d, flat col %d):\n%s",
			titleCol(tree, "subtask one"), titleCol(flat, "subtask one"), tree)
	}
}

// titleCol returns the column at which marker appears on its line — larger under
// indentation.
func titleCol(out, marker string) int {
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, marker); i >= 0 {
			return i
		}
	}
	return -1
}

// syncBuffer lets the watch goroutine and the test share an output buffer.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestE2EWatch(t *testing.T) {
	addr, tc := startServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var buf syncBuffer
	cmd := newRootCmd(&buf)
	cmd.SetArgs([]string{"--addr", addr, "watch"})
	cmd.SetErr(io.Discard)
	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()

	// The CLI swallows the handshake, so its subscription isn't observable
	// from the output; create canary tasks until one's event shows up.
	var canaryID string
	for range 20 {
		res, err := tc.CreateTask(ctx, connect.NewRequest(&taskpb.CreateTaskRequest{Title: "canary"}))
		if err != nil {
			t.Fatalf("create canary: %v", err)
		}
		id := res.Msg.GetTask().GetId()
		if waitFor(500*time.Millisecond, func() bool {
			return strings.Contains(buf.String(), "+ "+shortID(id)+" canary")
		}) {
			canaryID = id
			break
		}
	}
	if canaryID == "" {
		t.Fatalf("watch never reported a created task; output:\n%s", buf.String())
	}

	if _, err := tc.UpdateTask(ctx, connect.NewRequest(&taskpb.UpdateTaskRequest{
		Id:         canaryID,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"title"}},
		Task:       &taskpb.Task{Title: "canary v2"},
	})); err != nil {
		t.Fatalf("update canary: %v", err)
	}
	if _, err := tc.DeleteTask(ctx, connect.NewRequest(&taskpb.DeleteTaskRequest{Id: canaryID})); err != nil {
		t.Fatalf("delete canary: %v", err)
	}

	if !waitFor(5*time.Second, func() bool {
		out := buf.String()
		return strings.Contains(out, "~ "+shortID(canaryID)+" canary v2") &&
			strings.Contains(out, "- "+shortID(canaryID))
	}) {
		t.Fatalf("watch missed update/delete; output:\n%s", buf.String())
	}

	cancel()
	select {
	case <-done: // the cancellation error is expected; the lines matter
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not exit after context cancel")
	}
}
