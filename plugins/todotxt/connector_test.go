package todotxt

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
	todotxtpluginv1 "todoapp/gen/todotxtplugin/v1"
)

// fixedNow makes completion dates deterministic in golden tests.
var fixedNow = time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)

func newConn(t *testing.T, content string) (*Connector, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "todo.txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	c := &Connector{Now: func() time.Time { return fixedNow }}
	cfg, err := structpb.NewStruct(map[string]any{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Configure(context.Background(), "todo@test", cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}
	return c, path
}

func handle(t *testing.T, c *Connector, extID string, intent pluginv1.Intent, params map[string]any) (*pluginv1.RemoteItem, error) {
	t.Helper()
	var p *structpb.Struct
	if params != nil {
		var err error
		if p, err = structpb.NewStruct(params); err != nil {
			t.Fatal(err)
		}
	}
	return c.HandleIntent(context.Background(), &pluginv1.HandleIntentRequest{
		ExternalId:     extID,
		Intent:         intent,
		Params:         p,
		IdempotencyKey: "key-1",
	})
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func decodeTask(t *testing.T, it *pluginv1.RemoteItem) *todotxtpluginv1.Task {
	t.Helper()
	a := it.GetData()["task"]
	if a == nil {
		t.Fatalf("item %q has no task payload", it.GetExternalId())
	}
	var task todotxtpluginv1.Task
	if err := a.UnmarshalTo(&task); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	return &task
}

func TestConfigureValidation(t *testing.T) {
	c := &Connector{}
	bad := []struct {
		name    string
		cfg     map[string]any
		nilCfg  bool
		wantSub string
	}{
		{name: "nil", nilCfg: true, wantSub: "missing config"},
		{name: "missing path", cfg: map[string]any{}, wantSub: `"path" is missing`},
		{name: "relative path", cfg: map[string]any{"path": "todo.txt"}, wantSub: "absolute path"},
		{name: "nonexistent", cfg: map[string]any{"path": "/nonexistent/todo.txt"}, wantSub: "must exist"},
		{name: "unknown field", cfg: map[string]any{"path": "/x", "extra": 1}, wantSub: "unknown field(s) extra"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			var s *structpb.Struct
			if !tc.nilCfg {
				var err error
				if s, err = structpb.NewStruct(tc.cfg); err != nil {
					t.Fatal(err)
				}
			}
			_, err := c.Configure(context.Background(), "todo@test", s)
			if err == nil {
				t.Fatal("Configure accepted invalid config")
			}
			if status.Code(err) != codes.InvalidArgument {
				t.Errorf("code = %v, want InvalidArgument", status.Code(err))
			}
		})
	}

	// A directory path is rejected too.
	dir := t.TempDir()
	s, _ := structpb.NewStruct(map[string]any{"path": dir})
	if _, err := c.Configure(context.Background(), "todo@test", s); err == nil {
		t.Error("Configure accepted a directory path")
	}
}

func TestConfigureCapabilities(t *testing.T) {
	c, _ := newConn(t, "buy milk\n")
	// Reconfigure to inspect the returned capabilities.
	path := c.cfg.Path
	cfg, _ := structpb.NewStruct(map[string]any{"path": path})
	caps, err := c.Configure(context.Background(), "todo@test", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if caps.GetEnumeration() != pluginv1.Enumeration_ENUMERATION_SNAPSHOT {
		t.Errorf("enumeration = %v", caps.GetEnumeration())
	}
	if caps.GetPollInterval().AsDuration() != time.Minute {
		t.Errorf("poll = %v, want 1m", caps.GetPollInterval().AsDuration())
	}
	if len(caps.GetIntents()) != 1 || caps.GetIntents()[0].GetKind() != KindTask {
		t.Fatalf("intents = %v", caps.GetIntents())
	}
	got := caps.GetIntents()[0].GetIntents()
	want := []pluginv1.Intent{
		pluginv1.Intent_INTENT_RENAME,
		pluginv1.Intent_INTENT_SET_COMPLETED,
		pluginv1.Intent_INTENT_SET_DUE,
	}
	if len(got) != len(want) {
		t.Fatalf("intents = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("intents = %v, want %v", got, want)
		}
	}
}

func TestSnapshotGolden(t *testing.T) {
	content := readFile(t, filepath.Join("testdata", "sample.txt"))
	c, _ := newConn(t, content)

	var items []*pluginv1.RemoteItem
	if err := c.Snapshot(context.Background(), func(it *pluginv1.RemoteItem) error {
		items = append(items, it)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(items) != 5 {
		t.Fatalf("got %d items, want 5", len(items))
	}

	type want struct {
		id       string
		title    string
		state    string
		priority string
		projects []string
		contexts []string
		due      string // YYYY-MM-DD or ""
		created  string
		done     string
	}
	wants := []want{
		{id: hashID("call dentist +health @phone due:2026-07-10"), title: "call dentist", state: StateOpen, priority: "A", projects: []string{"health"}, contexts: []string{"phone"}, due: "2026-07-10"},
		{id: hashID("buy milk"), title: "buy milk", state: StateOpen},
		{id: hashID("write report +work pri:C"), title: "write report", state: StateDone, priority: "C", projects: []string{"work"}, created: "2026-06-01", done: "2026-06-30"},
		{id: "pr-42", title: "review the pr", state: StateOpen, contexts: []string{"code"}},
		{id: "inv-1", title: "pay invoice", state: StateOpen, due: "2026-07-15"},
	}

	fmtDate := func(ts interface{ AsTime() time.Time }) string {
		return ts.AsTime().UTC().Format(dateLayout)
	}
	for i, w := range wants {
		it := items[i]
		if it.GetExternalId() != w.id {
			t.Errorf("item %d external_id = %q, want %q", i, it.GetExternalId(), w.id)
		}
		if it.GetKind() != KindTask {
			t.Errorf("item %d kind = %q", i, it.GetKind())
		}
		if it.GetTitle() != w.title {
			t.Errorf("item %d title = %q, want %q", i, it.GetTitle(), w.title)
		}
		if it.GetState() != w.state {
			t.Errorf("item %d state = %q, want %q", i, it.GetState(), w.state)
		}
		task := decodeTask(t, it)
		if task.GetPriority() != w.priority {
			t.Errorf("item %d priority = %q, want %q", i, task.GetPriority(), w.priority)
		}
		if !eqStrings(task.GetProjects(), w.projects) {
			t.Errorf("item %d projects = %v, want %v", i, task.GetProjects(), w.projects)
		}
		if !eqStrings(task.GetContexts(), w.contexts) {
			t.Errorf("item %d contexts = %v, want %v", i, task.GetContexts(), w.contexts)
		}
		if w.due == "" {
			if task.GetDue() != nil {
				t.Errorf("item %d due = %v, want none", i, task.GetDue().AsTime())
			}
		} else if got := fmtDate(task.GetDue()); got != w.due {
			t.Errorf("item %d due = %q, want %q", i, got, w.due)
		}
		if w.created != "" && fmtDate(task.GetCreatedOn()) != w.created {
			t.Errorf("item %d created = %q, want %q", i, fmtDate(task.GetCreatedOn()), w.created)
		}
		if w.done != "" && fmtDate(task.GetCompletedOn()) != w.done {
			t.Errorf("item %d completed = %q, want %q", i, fmtDate(task.GetCompletedOn()), w.done)
		}
	}
}

func TestSnapshotSkipsDuplicateUntaggedText(t *testing.T) {
	c, _ := newConn(t, "buy milk\nbuy milk\ndo laundry\n")
	var ids []string
	if err := c.Snapshot(context.Background(), func(it *pluginv1.RemoteItem) error {
		ids = append(ids, it.GetExternalId())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("got %d items, want 2 (the duplicate skipped): %v", len(ids), ids)
	}
	if ids[0] != hashID("buy milk") || ids[1] != hashID("do laundry") {
		t.Fatalf("ids = %v", ids)
	}
}

func TestHandleIntentRenamePreservesTags(t *testing.T) {
	content := "buy milk +groceries @store due:2026-07-10\nsecond line unchanged\n"
	c, path := newConn(t, content)
	id := hashID("buy milk +groceries @store due:2026-07-10")

	it, err := handle(t, c, id, pluginv1.Intent_INTENT_RENAME, map[string]any{"title": "purchase groceries"})
	if err != nil {
		t.Fatal(err)
	}

	wantFile := "purchase groceries +groceries @store due:2026-07-10 id:" + id + "\nsecond line unchanged\n"
	if got := readFile(t, path); got != wantFile {
		t.Fatalf("file =\n%q\nwant\n%q", got, wantFile)
	}
	if it.GetExternalId() != id {
		t.Errorf("returned external_id = %q, want %q (identity must survive rename)", it.GetExternalId(), id)
	}
	if it.GetTitle() != "purchase groceries" {
		t.Errorf("returned title = %q", it.GetTitle())
	}
}

func TestHandleIntentRenameEmptyTitle(t *testing.T) {
	c, _ := newConn(t, "buy milk id:m1\n")
	_, err := handle(t, c, "m1", pluginv1.Intent_INTENT_RENAME, map[string]any{"title": "   "})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestHandleIntentSetDue(t *testing.T) {
	content := "buy milk due:2026-07-10 id:milk-1\nkeep me\n"

	// Set.
	c, path := newConn(t, content)
	it, err := handle(t, c, "milk-1", pluginv1.Intent_INTENT_SET_DUE, map[string]any{"due": "2026-07-20T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, path), "buy milk due:2026-07-20 id:milk-1\nkeep me\n"; got != want {
		t.Fatalf("set: file = %q, want %q", got, want)
	}
	if got := decodeTask(t, it).GetDue().AsTime().UTC().Format(dateLayout); got != "2026-07-20" {
		t.Errorf("returned due = %q, want 2026-07-20", got)
	}

	// Clear (no due key).
	c2, path2 := newConn(t, content)
	it2, err := handle(t, c2, "milk-1", pluginv1.Intent_INTENT_SET_DUE, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, path2), "buy milk id:milk-1\nkeep me\n"; got != want {
		t.Fatalf("clear: file = %q, want %q", got, want)
	}
	if decodeTask(t, it2).GetDue() != nil {
		t.Errorf("returned due should be nil after clear")
	}
}

func TestHandleIntentSetCompletedTrue(t *testing.T) {
	content := "(A) file taxes +finance id:tax-1\n"
	c, path := newConn(t, content)

	it, err := handle(t, c, "tax-1", pluginv1.Intent_INTENT_SET_COMPLETED, map[string]any{"completed": true})
	if err != nil {
		t.Fatal(err)
	}
	wantFile := "x 2026-07-03 file taxes +finance id:tax-1 pri:A\n"
	if got := readFile(t, path); got != wantFile {
		t.Fatalf("file = %q, want %q", got, wantFile)
	}
	if it.GetState() != StateDone {
		t.Errorf("state = %q, want done", it.GetState())
	}
	task := decodeTask(t, it)
	if task.GetPriority() != "A" {
		t.Errorf("priority = %q, want A (preserved as pri: tag)", task.GetPriority())
	}
	if got := task.GetCompletedOn().AsTime().UTC().Format(dateLayout); got != "2026-07-03" {
		t.Errorf("completed_on = %q, want 2026-07-03", got)
	}
}

func TestHandleIntentSetCompletedFalseRestoresPriority(t *testing.T) {
	content := "x 2026-07-03 file taxes +finance id:tax-1 pri:A\n"
	c, path := newConn(t, content)

	it, err := handle(t, c, "tax-1", pluginv1.Intent_INTENT_SET_COMPLETED, map[string]any{"completed": false})
	if err != nil {
		t.Fatal(err)
	}
	wantFile := "(A) file taxes +finance id:tax-1\n"
	if got := readFile(t, path); got != wantFile {
		t.Fatalf("file = %q, want %q", got, wantFile)
	}
	if it.GetState() != StateOpen {
		t.Errorf("state = %q, want open", it.GetState())
	}
	if got := decodeTask(t, it).GetPriority(); got != "A" {
		t.Errorf("priority = %q, want A (restored to (A) form)", got)
	}
}

func TestIDStabilizationOnUntaggedLine(t *testing.T) {
	content := "walk the dog +health\nkeep\n"
	c, path := newConn(t, content)
	id := hashID("walk the dog +health")

	it, err := handle(t, c, id, pluginv1.Intent_INTENT_SET_DUE, map[string]any{"due": "2026-07-05T12:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	wantFile := "walk the dog +health id:" + id + " due:2026-07-05\nkeep\n"
	if got := readFile(t, path); got != wantFile {
		t.Fatalf("file = %q, want %q", got, wantFile)
	}
	// The identity the caller addressed survives the edit that changed the text.
	if it.GetExternalId() != id {
		t.Errorf("external_id = %q, want %q", it.GetExternalId(), id)
	}
}

func TestIntentIdempotency(t *testing.T) {
	content := "buy milk\nother\n"
	c, path := newConn(t, content)
	id := hashID("buy milk")

	if _, err := handle(t, c, id, pluginv1.Intent_INTENT_SET_COMPLETED, map[string]any{"completed": true}); err != nil {
		t.Fatal(err)
	}
	after1 := readFile(t, path)

	// Same intent, same idempotency key. The line is now tagged, so it is
	// located by its id: tag; the result must be byte-identical.
	if _, err := handle(t, c, id, pluginv1.Intent_INTENT_SET_COMPLETED, map[string]any{"completed": true}); err != nil {
		t.Fatal(err)
	}
	after2 := readFile(t, path)

	if after1 != after2 {
		t.Fatalf("not idempotent:\n first  %q\n second %q", after1, after2)
	}
	wantFile := "x 2026-07-03 buy milk id:" + id + "\nother\n"
	if after1 != wantFile {
		t.Fatalf("file = %q, want %q", after1, wantFile)
	}
}

func TestHandleIntentNotFound(t *testing.T) {
	c, _ := newConn(t, "buy milk\n")
	_, err := handle(t, c, "does-not-exist", pluginv1.Intent_INTENT_RENAME, map[string]any{"title": "x"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("code = %v, want NotFound", status.Code(err))
	}
}

func TestResolve(t *testing.T) {
	content := "buy milk id:m1\nplain task here\n"
	c, _ := newConn(t, content)
	ctx := context.Background()

	// By id tag.
	it, err := c.Resolve(ctx, "m1")
	if err != nil || it.GetExternalId() != "m1" {
		t.Fatalf("resolve by id: %v, %v", it, err)
	}
	// By hash.
	hid := hashID("plain task here")
	it, err = c.Resolve(ctx, hid)
	if err != nil || it.GetExternalId() != hid {
		t.Fatalf("resolve by hash: %v, %v", it, err)
	}
	// By literal task text.
	it, err = c.Resolve(ctx, "plain task here")
	if err != nil || it.GetExternalId() != hid {
		t.Fatalf("resolve by text: %v, %v", it, err)
	}
	// Miss.
	if _, err := c.Resolve(ctx, "nope"); status.Code(err) != codes.NotFound {
		t.Fatalf("resolve miss code = %v, want NotFound", status.Code(err))
	}
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
