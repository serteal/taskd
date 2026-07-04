package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/encoding/protojson"

	taskpb "github.com/serteal/taskd/gen/task"
	"github.com/serteal/taskd/gen/task/taskconnect"
	"github.com/serteal/taskd/internal/server"
	"github.com/serteal/taskd/internal/store"
)

// newSession serves a real store over a real HTTP server (the pattern from
// internal/server's tests), points a bridge at it, and connects an
// in-process MCP client over the SDK's in-memory transports.
func newSession(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	path, handler := server.New(st).Handler()
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	b := &bridge{tc: taskconnect.NewTaskServiceClient(ts.Client(), ts.URL)}

	// The server transport must be connected before the client transport: the
	// client initializes the MCP session during its own connect.
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := b.server().Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { ss.Close() })

	cl := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	sess, err := cl.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess
}

// call invokes a tool. A transport-level error fails the test; tool-level
// errors are reported through res.IsError, which callers inspect.
func call(t *testing.T, sess *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return res
}

// ok invokes a tool and fails the test on a tool error.
func ok(t *testing.T, sess *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res := call(t, sess, name, args)
	if res.IsError {
		t.Fatalf("%s(%v) failed: %s", name, args, resultText(res))
	}
	return res
}

func resultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// structured re-serializes a result's structured content so it can be
// decoded into concrete types.
func structured(t *testing.T, res *mcp.CallToolResult) []byte {
	t.Helper()
	if res.StructuredContent == nil {
		t.Fatalf("result has no structured content (text: %s)", resultText(res))
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	return raw
}

// asTask decodes a single-task structured result via protojson.
func asTask(t *testing.T, res *mcp.CallToolResult) *taskpb.Task {
	t.Helper()
	var tk taskpb.Task
	if err := protojson.Unmarshal(structured(t, res), &tk); err != nil {
		t.Fatalf("unmarshal task from %s: %v", structured(t, res), err)
	}
	return &tk
}

// asTasks decodes a list_tasks structured result: {"tasks": [...]}.
func asTasks(t *testing.T, res *mcp.CallToolResult) []*taskpb.Task {
	t.Helper()
	var wrap struct {
		Tasks []json.RawMessage `json:"tasks"`
	}
	if err := json.Unmarshal(structured(t, res), &wrap); err != nil {
		t.Fatalf("unmarshal task list: %v", err)
	}
	tasks := make([]*taskpb.Task, len(wrap.Tasks))
	for i, raw := range wrap.Tasks {
		tasks[i] = &taskpb.Task{}
		if err := protojson.Unmarshal(raw, tasks[i]); err != nil {
			t.Fatalf("unmarshal task %d from %s: %v", i, raw, err)
		}
	}
	return tasks
}

func titles(tasks []*taskpb.Task) []string {
	out := make([]string, len(tasks))
	for i, tk := range tasks {
		out[i] = tk.GetTitle()
	}
	return out
}

func TestToolsList(t *testing.T) {
	sess := newSession(t)
	res, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"list_tasks", "get_task", "create_task", "update_task",
		"complete_task", "reopen_task", "delete_task", "list_labels",
	}
	got := map[string]string{}
	for _, tl := range res.Tools {
		got[tl.Name] = tl.Description
	}
	for _, name := range want {
		desc, found := got[name]
		if !found {
			t.Errorf("tools/list missing %q", name)
			continue
		}
		if strings.TrimSpace(desc) == "" {
			t.Errorf("tool %q has an empty description", name)
		}
	}
	if len(got) != len(want) {
		t.Errorf("tool count = %d, want %d (%v)", len(got), len(want), got)
	}
	// The descriptions are the API docs: they must teach the label
	// conventions and the agent-labeling convention.
	for _, frag := range []string{"p1", "project:", "context:", "RFC3339"} {
		if !strings.Contains(got["list_tasks"], frag) {
			t.Errorf("list_tasks description must mention %q", frag)
		}
	}
	if !strings.Contains(got["create_task"], "agent:") {
		t.Errorf("create_task description must tell agents to label with agent:<name>")
	}
}

func TestLifecycle(t *testing.T) {
	sess := newSession(t)
	due := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

	// Create.
	created := asTask(t, ok(t, sess, "create_task", map[string]any{
		"title":    "Buy milk",
		"notes":    "2 liters",
		"labels":   []string{"p1", "project:home", "agent:claude"},
		"due_time": due.Format(time.RFC3339),
	}))
	if created.GetId() == "" || created.GetRevision() == 0 {
		t.Fatalf("created task missing id/revision: %v", created)
	}
	if created.GetTitle() != "Buy milk" || created.GetNotes() != "2 liters" {
		t.Errorf("created = %q / %q, want Buy milk / 2 liters", created.GetTitle(), created.GetNotes())
	}
	if len(created.GetLabels()) != 3 {
		t.Errorf("labels = %v, want 3", created.GetLabels())
	}
	if !created.GetDueTime().AsTime().Equal(due) {
		t.Errorf("due = %v, want %v", created.GetDueTime().AsTime(), due)
	}
	ok(t, sess, "create_task", map[string]any{"title": "Walk dog", "labels": []string{"project:home"}})

	// A malformed timestamp is a clean tool error naming the format.
	if bad := call(t, sess, "create_task", map[string]any{"title": "x", "due_time": "next tuesday"}); !bad.IsError {
		t.Errorf("expected a tool error for a non-RFC3339 due_time")
	} else if !strings.Contains(resultText(bad), "RFC3339") {
		t.Errorf("bad-timestamp error should name RFC3339, got %q", resultText(bad))
	}

	// List: labels_all narrows conjunctively.
	if got := asTasks(t, ok(t, sess, "list_tasks", map[string]any{"labels_all": []string{"project:home"}})); len(got) != 2 {
		t.Errorf("labels_all=[project:home] = %v, want 2 tasks", titles(got))
	}
	got := asTasks(t, ok(t, sess, "list_tasks", map[string]any{"labels_all": []string{"p1", "project:home"}}))
	if len(got) != 1 || got[0].GetTitle() != "Buy milk" {
		t.Errorf("labels_all=[p1 project:home] = %v, want [Buy milk]", titles(got))
	}
	// limit truncates.
	if got := asTasks(t, ok(t, sess, "list_tasks", map[string]any{"limit": 1})); len(got) != 1 {
		t.Errorf("limit=1 returned %d tasks", len(got))
	}

	// Update mask correctness: only the provided field changes.
	updated := asTask(t, ok(t, sess, "update_task", map[string]any{
		"id":    created.GetId(),
		"notes": "2 liters, oat",
	}))
	if updated.GetNotes() != "2 liters, oat" {
		t.Errorf("notes = %q after update", updated.GetNotes())
	}
	if updated.GetTitle() != "Buy milk" || len(updated.GetLabels()) != 3 || !updated.GetDueTime().AsTime().Equal(due) {
		t.Errorf("update touched unprovided fields: %v", updated)
	}

	// Labels REPLACE the whole set.
	updated = asTask(t, ok(t, sess, "update_task", map[string]any{
		"id":     created.GetId(),
		"labels": []string{"p2", "project:home"},
	}))
	if got := updated.GetLabels(); len(got) != 2 || got[0] != "p2" || got[1] != "project:home" {
		t.Errorf("labels after replace = %v, want [p2 project:home]", got)
	}

	// clear_due removes the due date and nothing else.
	updated = asTask(t, ok(t, sess, "update_task", map[string]any{
		"id":        created.GetId(),
		"clear_due": true,
	}))
	if updated.GetDueTime() != nil {
		t.Errorf("due survived clear_due: %v", updated.GetDueTime())
	}
	if updated.GetNotes() != "2 liters, oat" {
		t.Errorf("clear_due touched notes: %q", updated.GetNotes())
	}

	// A stale expected_revision surfaces as a re-read instruction.
	stale := call(t, sess, "update_task", map[string]any{
		"id":                created.GetId(),
		"title":             "Buy oat milk",
		"expected_revision": 1,
	})
	if !stale.IsError {
		t.Fatalf("stale expected_revision should be a tool error")
	}
	if msg := resultText(stale); !strings.Contains(msg, "get_task") {
		t.Errorf("conflict error should tell the agent to re-read via get_task, got %q", msg)
	}

	// Complete, then filter by completion state.
	done := asTask(t, ok(t, sess, "complete_task", map[string]any{"id": created.GetId()}))
	if done.GetCompletedTime() == nil {
		t.Fatalf("complete_task did not stamp completed_time")
	}
	if got := asTasks(t, ok(t, sess, "list_tasks", map[string]any{"labels_all": []string{"project:home"}})); len(got) != 2 {
		t.Errorf("completed omitted should list active AND completed, got %v", titles(got))
	}
	got = asTasks(t, ok(t, sess, "list_tasks", map[string]any{"completed": false}))
	if len(got) != 1 || got[0].GetTitle() != "Walk dog" {
		t.Errorf("completed=false = %v, want [Walk dog]", titles(got))
	}
	got = asTasks(t, ok(t, sess, "list_tasks", map[string]any{"completed": true}))
	if len(got) != 1 || got[0].GetTitle() != "Buy milk" {
		t.Errorf("completed=true = %v, want [Buy milk]", titles(got))
	}

	// Reopen clears completed_time.
	if reopened := asTask(t, ok(t, sess, "reopen_task", map[string]any{"id": created.GetId()})); reopened.GetCompletedTime() != nil {
		t.Errorf("reopen_task left completed_time set: %v", reopened.GetCompletedTime())
	}
}

func TestDeleteGuard(t *testing.T) {
	sess := newSession(t)
	id := asTask(t, ok(t, sess, "create_task", map[string]any{"title": "victim"})).GetId()

	// Without the opt-in: refused before any RPC, steering to complete_task.
	t.Setenv("TASKMCP_ALLOW_DESTRUCTIVE", "")
	res := call(t, sess, "delete_task", map[string]any{"id": id})
	if !res.IsError {
		t.Fatalf("delete_task should be refused without TASKMCP_ALLOW_DESTRUCTIVE=1")
	}
	msg := resultText(res)
	if !strings.Contains(msg, "TASKMCP_ALLOW_DESTRUCTIVE") {
		t.Errorf("refusal should cite the opt-in env var, got %q", msg)
	}
	if !strings.Contains(msg, "complete_task") {
		t.Errorf("refusal should point at complete_task, got %q", msg)
	}
	ok(t, sess, "get_task", map[string]any{"id": id}) // still alive

	// With the opt-in: deletion goes through and the task is gone.
	t.Setenv("TASKMCP_ALLOW_DESTRUCTIVE", "1")
	var out struct {
		ID      string `json:"id"`
		Deleted bool   `json:"deleted"`
	}
	if err := json.Unmarshal(structured(t, ok(t, sess, "delete_task", map[string]any{"id": id})), &out); err != nil {
		t.Fatalf("delete result: %v", err)
	}
	if out.ID != id || !out.Deleted {
		t.Errorf("delete result = %+v, want deleted %s", out, id)
	}
	gone := call(t, sess, "get_task", map[string]any{"id": id})
	if !gone.IsError {
		t.Fatalf("get_task after delete should fail")
	}
	if msg := resultText(gone); !strings.Contains(msg, "not_found") {
		t.Errorf("post-delete get should surface not_found, got %q", msg)
	}
}

func TestListLabels(t *testing.T) {
	sess := newSession(t)
	ok(t, sess, "create_task", map[string]any{"title": "A", "labels": []string{"project:home", "p1"}})
	doneID := asTask(t, ok(t, sess, "create_task", map[string]any{"title": "B", "labels": []string{"project:home"}})).GetId()
	ok(t, sess, "complete_task", map[string]any{"id": doneID})

	counts := func(args map[string]any) map[string]int64 {
		t.Helper()
		var wrap struct {
			Labels []struct {
				Label string `json:"label"`
				Count int64  `json:"count"`
			} `json:"labels"`
		}
		if err := json.Unmarshal(structured(t, ok(t, sess, "list_labels", args)), &wrap); err != nil {
			t.Fatalf("unmarshal labels: %v", err)
		}
		m := map[string]int64{}
		for _, lc := range wrap.Labels {
			m[lc.Label] = lc.Count
		}
		return m
	}

	active := counts(nil)
	if active["project:home"] != 1 || active["p1"] != 1 {
		t.Errorf("active label counts = %v, want project:home=1 p1=1", active)
	}
	all := counts(map[string]any{"include_completed": true})
	if all["project:home"] != 2 || all["p1"] != 1 {
		t.Errorf("include_completed counts = %v, want project:home=2 p1=1", all)
	}
}
