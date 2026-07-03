package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/encoding/protojson"

	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/daemon"
	"todoapp/pkg/taskclient"
)

// NOTE: test names are deliberately short — t.TempDir() feeds the unix socket
// path, and macOS caps those at 104 bytes.

// startDaemon runs taskd in-process against a fresh temp dir and returns it;
// the daemon stops (and its error is checked) at cleanup.
func startDaemon(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	go func() { errc <- daemon.Run(ctx, daemon.Config{Dir: dir, Log: log}) }()
	t.Cleanup(func() {
		cancel()
		if err := <-errc; err != nil {
			t.Errorf("daemon exit: %v", err)
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(daemon.SocketPath(dir)); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("socket never appeared")
		}
		time.Sleep(20 * time.Millisecond)
	}
	return dir
}

// connect builds the bridge against the daemon at dir and connects an MCP
// client to it over the SDK's in-memory transports. Returns the client
// session (torn down at cleanup).
func connect(t *testing.T, dir string) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	sock := daemon.SocketPath(dir)

	cl, err := taskclient.Dial(ctx, sock, clientName)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { cl.Close() })
	if _, err := cl.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	conn, err := dialExtra(sock)
	if err != nil {
		t.Fatalf("dialExtra: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	b := &bridge{
		cl:      cl,
		intents: taskcorev1.NewIntentServiceClient(conn),
		rules:   taskcorev1.NewRuleServiceClient(conn),
	}
	srv := b.server()

	// The server transport must be connected before the client transport: the
	// client initializes the MCP session during its own connect.
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { ss.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	sess, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess
}

// callTool invokes a tool and returns the result plus its concatenated text
// content. A CallTool transport error fails the test; tool-level errors are
// reported through res.IsError, which callers inspect.
func callTool(t *testing.T, sess *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return res, resultText(res)
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

// parseItem unmarshals the first protojson line of txt into an Item.
func parseItem(t *testing.T, txt string) *taskcorev1.Item {
	t.Helper()
	line := strings.SplitN(strings.TrimSpace(txt), "\n", 2)[0]
	it := &taskcorev1.Item{}
	if err := protojson.Unmarshal([]byte(line), it); err != nil {
		t.Fatalf("parse item %q: %v", line, err)
	}
	return it
}

// Scenario 1: tools/list lists every tool with a non-empty description, and
// query_items teaches the filter language.
func TestToolsList(t *testing.T) {
	sess := connect(t, startDaemon(t))
	res, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"query_items", "get_item", "create_task", "update_todo",
		"complete_task", "reopen_task", "invoke_intent", "list_pending",
		"list_rules", "list_views", "list_kinds",
	}
	got := map[string]string{}
	for _, tl := range res.Tools {
		got[tl.Name] = tl.Description
	}
	for _, name := range want {
		desc, ok := got[name]
		if !ok {
			t.Errorf("tools/list missing %q", name)
			continue
		}
		if strings.TrimSpace(desc) == "" {
			t.Errorf("tool %q has empty description", name)
		}
	}
	if len(got) != len(want) {
		t.Errorf("tool count = %d, want %d (%v)", len(got), len(want), got)
	}
	qd := got["query_items"]
	if !strings.Contains(qd, "completed") {
		t.Errorf("query_items description must mention the 'completed' variable")
	}
	if !strings.Contains(qd, "!completed") {
		t.Errorf("query_items description must include an example filter")
	}
}

// Scenario 2: create_task, active query, complete_task, and completion state.
func TestCreateCompleteFlow(t *testing.T) {
	sess := connect(t, startDaemon(t))

	_, txt := callTool(t, sess, "create_task", map[string]any{"title": "pay rent", "project": "home"})
	created := parseItem(t, txt)
	id := created.GetId()
	if created.GetTodo().GetTitleOverride() != "pay rent" {
		t.Fatalf("title = %q", created.GetTodo().GetTitleOverride())
	}
	if created.GetTodo().GetProject() != "home" {
		t.Fatalf("project = %q", created.GetTodo().GetProject())
	}

	_, active := callTool(t, sess, "query_items", map[string]any{"filter": "!completed"})
	if !strings.Contains(active, id) {
		t.Fatalf("active query is missing the new item %s:\n%s", id, active)
	}

	if res, msg := callTool(t, sess, "complete_task", map[string]any{"id": id}); res.IsError {
		t.Fatalf("complete_task failed: %s", msg)
	}

	_, active2 := callTool(t, sess, "query_items", map[string]any{"filter": "!completed"})
	if strings.Contains(active2, id) {
		t.Fatalf("completed item %s still shows in the active query", id)
	}

	_, g := callTool(t, sess, "get_item", map[string]any{"id": id})
	done := parseItem(t, g)
	if !done.GetTodo().GetCompleted() {
		t.Errorf("get_item: completed = false, want true")
	}
	if done.GetTodo().GetCompletedAt() == nil {
		t.Errorf("get_item: completed_at not stamped")
	}
}

// Scenario 3: update_todo builds a mask from present fields; a bad RFC3339 due
// yields a clean tool error.
func TestUpdateTodo(t *testing.T) {
	sess := connect(t, startDaemon(t))
	_, txt := callTool(t, sess, "create_task", map[string]any{"title": "review PR"})
	id := parseItem(t, txt).GetId()

	res, msg := callTool(t, sess, "update_todo", map[string]any{
		"id": id,
		"set": map[string]any{
			"labels":  []string{"urgent", "work"},
			"project": "work/reviews",
		},
	})
	if res.IsError {
		t.Fatalf("update_todo failed: %s", msg)
	}
	updated := parseItem(t, msg)
	if updated.GetTodo().GetProject() != "work/reviews" {
		t.Errorf("project = %q, want work/reviews", updated.GetTodo().GetProject())
	}
	if labels := updated.GetTodo().GetLabels(); len(labels) != 2 {
		t.Errorf("labels = %v, want two", labels)
	}

	bad, badMsg := callTool(t, sess, "update_todo", map[string]any{
		"id":  id,
		"set": map[string]any{"due_rfc3339": "next tuesday"},
	})
	if !bad.IsError {
		t.Fatalf("expected a tool error for a bad RFC3339 due")
	}
	if !strings.Contains(badMsg, "RFC3339") {
		t.Errorf("error should name the expected format, got %q", badMsg)
	}
}

// Scenario 4: the destructive guard refuses "delete" client-side without an
// allowlist, and lets it through to the daemon with one.
func TestInvokeIntentGuard(t *testing.T) {
	sess := connect(t, startDaemon(t))
	_, txt := callTool(t, sess, "create_task", map[string]any{"title": "native task"})
	id := parseItem(t, txt).GetId()

	// Without the allowlist: refused before any RPC. The refusal cites the
	// policy env var and must NOT carry a daemon-side error.
	res, msg := callTool(t, sess, "invoke_intent", map[string]any{"item_id": id, "intent": "delete"})
	if !res.IsError {
		t.Fatalf("delete should be refused without an allowlist")
	}
	if !strings.Contains(msg, "TASKMCP_ALLOW_DESTRUCTIVE") {
		t.Errorf("refusal should cite the policy env var, got %q", msg)
	}
	if strings.Contains(msg, "FailedPrecondition") || strings.Contains(strings.ToLower(msg), "no remote") {
		t.Errorf("refusal leaked a daemon error (RPC should not have happened): %q", msg)
	}

	// With the allowlist: the guard passes, the RPC reaches the daemon, and a
	// native item (no mirror) fails with FailedPrecondition/not-mirrored —
	// a DIFFERENT error, proving the guard let the call through.
	t.Setenv("TASKMCP_ALLOW_DESTRUCTIVE", "delete")
	res2, msg2 := callTool(t, sess, "invoke_intent", map[string]any{"item_id": id, "intent": "delete"})
	if !res2.IsError {
		t.Fatalf("expected the daemon's not-mirrored error once the guard passes")
	}
	if strings.Contains(msg2, "TASKMCP_ALLOW_DESTRUCTIVE") {
		t.Errorf("with the allowlist this should no longer be a refusal, got %q", msg2)
	}
	if !strings.Contains(msg2, "FailedPrecondition") && !strings.Contains(strings.ToLower(msg2), "remote") {
		t.Errorf("expected the daemon's not-mirrored error, got %q", msg2)
	}
}

// Scenario 5: the views resource lists the well-known views, and the view
// template returns a view's items.
func TestResources(t *testing.T) {
	dir := startDaemon(t)
	sess := connect(t, dir)

	_, txt := callTool(t, sess, "create_task", map[string]any{"title": "archive me"})
	id := parseItem(t, txt).GetId()
	if res, msg := callTool(t, sess, "complete_task", map[string]any{"id": id}); res.IsError {
		t.Fatalf("complete_task failed: %s", msg)
	}

	views, err := sess.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "taskcore://views"})
	if err != nil {
		t.Fatal(err)
	}
	vtext := views.Contents[0].Text
	for _, name := range []string{"inbox", "today", "completed"} {
		if !strings.Contains(vtext, name) {
			t.Errorf("taskcore://views is missing well-known view %q:\n%s", name, vtext)
		}
	}

	completed, err := sess.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "taskcore://view/completed"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(completed.Contents[0].Text, id) {
		t.Errorf("taskcore://view/completed should list the completed item %s:\n%s", id, completed.Contents[0].Text)
	}
}

// Scenario 6: the native kind "task" appears in both the tool and the schema
// resource.
func TestListKinds(t *testing.T) {
	sess := connect(t, startDaemon(t))

	_, kinds := callTool(t, sess, "list_kinds", map[string]any{})
	if !strings.Contains(kinds, "task") {
		t.Errorf("list_kinds tool is missing kind \"task\":\n%s", kinds)
	}

	schema, err := sess.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "taskcore://schema"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(schema.Contents[0].Text, "task") {
		t.Errorf("taskcore://schema is missing kind \"task\":\n%s", schema.Contents[0].Text)
	}
}
