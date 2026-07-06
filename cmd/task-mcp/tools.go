package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskpb "github.com/serteal/taskd/gen/task"
	"github.com/serteal/taskd/internal/recur"
)

// defaultListLimit is how many tasks list_tasks returns when the caller does
// not say; the handler pages through the daemon internally up to the limit.
const defaultListLimit = 100

// registerTools wires every tool to its typed handler. AddTool infers the
// JSON Schema of each input struct from its jsonschema tags, so agents get
// self-describing tools; results carry structured JSON (protojson for
// tasks), not prose.
func (b *bridge) registerTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "list_tasks", Description: listTasksDesc}, b.listTasks)
	mcp.AddTool(s, &mcp.Tool{Name: "get_task", Description: getTaskDesc}, b.getTask)
	mcp.AddTool(s, &mcp.Tool{Name: "create_task", Description: createTaskDesc}, b.createTask)
	mcp.AddTool(s, &mcp.Tool{Name: "update_task", Description: updateTaskDesc}, b.updateTask)
	mcp.AddTool(s, &mcp.Tool{Name: "complete_task", Description: completeTaskDesc}, b.completeTask)
	mcp.AddTool(s, &mcp.Tool{Name: "reopen_task", Description: reopenTaskDesc}, b.reopenTask)
	mcp.AddTool(s, &mcp.Tool{Name: "delete_task", Description: deleteTaskDesc}, b.deleteTask)
	mcp.AddTool(s, &mcp.Tool{Name: "list_labels", Description: listLabelsDesc}, b.listLabels)
}

const listTasksDesc = `List tasks matching a structured filter. Every filter dimension you set must match (AND); an empty filter matches all tasks. Returns {"tasks": [...]} with each task as JSON.

Label conventions — tasks classify by labels plus an optional due date, nothing else:
- priorities are the labels "p1" (highest), "p2", "p3"
- projects are "project:<name>", e.g. "project:home"
- contexts are "context:<name>", e.g. "context:errands"
Label matching is exact and case-sensitive. labels_any matches tasks carrying at least one of the given labels; labels_all requires all of them.

Other dimensions:
- completed: omit to get active AND completed tasks; true for only completed; false for only active.
- due_before / due_after: RFC3339 timestamps forming a half-open range (due_after <= due_time < due_before); either bound may be set alone. Tasks without a due date never match a bound.
- has_due: true for only tasks with a due date, false for only tasks without one.
- source: exact match on the syncing integration name (e.g. "github", "ics:work"); pass "" to match only local, non-synced tasks; omit to match any source.
- text: case-insensitive substring match over title and notes.

Ownership note: synced tasks (source != "") have title, due_time, and completed_time owned by their source system and overwritten on every sync — labels and notes are the user-owned fields an agent should annotate.`

const getTaskDesc = `Fetch one task by id. Returns the complete task as JSON, including external_data — the source-specific detail (PR author, event location, ...) carried verbatim for synced tasks.`

const createTaskDesc = `Create a local task. Only title is required. Classify with labels: priorities are "p1"/"p2"/"p3", projects "project:<name>", contexts "context:<name>". due_time is an RFC3339 timestamp.

When you create a task for yourself as an agent (a reminder, a follow-up), add the label "agent:<agent-name>" (e.g. "agent:claude") so humans can find and filter agent-created tasks.

recurrence makes the task repeat. Give it as natural language ("every day", "every weekday", "every 2 weeks", "mon,wed,fri") or a canonical RRULE subset (FREQ=DAILY|WEEKLY|MONTHLY|YEARLY, optional INTERVAL=n, optional BYDAY=MO..SU for weekly). Completing a recurring task does NOT close it: it archives a completed copy and advances the live task's due_time to the next occurrence (see complete_task).

parent_id nests this task under an existing parent task, forming a checklist. Hierarchy is one level deep: the parent must itself be top-level, and a task that already has children cannot be given a parent.

This creates local tasks only (source = ""); synced tasks arrive via their source integration, which owns their title/due/completed — on those, labels and notes are the fields to annotate. Returns the created task as JSON, including its server-assigned id and revision.`

const updateTaskDesc = `Update a task, changing ONLY the fields you provide — a field mask is built from the present keys, so absent fields are untouched.

- labels REPLACES the entire label set. To add or remove a single label, call get_task first and send back the full modified set.
- due_time sets the due date (RFC3339). clear_due removes it. Provide at most one of the two.
- recurrence sets or changes the repeat rule (natural language like "every weekday" or a canonical RRULE subset); an empty string stops the task recurring. Only allowed on local tasks.
- parent_id nests the task under a parent (one level deep: the parent must be top-level, and a task with children of its own cannot be given a parent); an empty string detaches it back to top-level.
- expected_revision: pass the revision from a previous read to make the update fail instead of silently overwriting if the task changed in between; on that conflict, re-read with get_task and retry.
- On synced tasks (source != "") title and due_time are owned by the source system and will be overwritten on the next sync; labels and notes are safe to edit.

Returns the updated task as JSON.`

const completeTaskDesc = `Mark a task done: sets completed_time to now. This is the normal way to finish with a task — prefer it over delete_task.

If the task recurs (recurrence set), it is NOT closed: the server archives a completed copy and rolls the live task forward to its next due date, so the returned task is the still-active series with an advanced due_time (an overdue recurring task advances just once, to the next future occurrence). Returns the updated task as JSON.`

const reopenTaskDesc = `Reopen a completed task: clears completed_time, making it active again. Returns the updated task as JSON.`

const deleteTaskDesc = `PERMANENTLY delete a task — destructive and irreversible. Almost always the wrong tool: complete_task marks a task done while keeping its history. Deletion is refused unless the task-mcp process was started with TASKMCP_ALLOW_DESTRUCTIVE=1.`

const listLabelsDesc = `List every label in use with its task count, as {"labels": [{"label": ..., "count": ...}]}. By default counts cover active tasks only and labels used solely by completed tasks are omitted; set include_completed to count completed tasks too. Useful for discovering the user's projects ("project:*"), contexts, and priority conventions before filtering or labeling.`

// --- input types ------------------------------------------------------------

// Pointer fields distinguish "omitted" from an explicit zero value (false, "",
// []), which the filter and mask semantics depend on.

type listTasksInput struct {
	LabelsAny []string `json:"labels_any,omitempty" jsonschema:"match tasks carrying at least one of these labels"`
	LabelsAll []string `json:"labels_all,omitempty" jsonschema:"match tasks carrying all of these labels"`
	Completed *bool    `json:"completed,omitempty" jsonschema:"omit for active and completed; true for only completed; false for only active"`
	DueBefore string   `json:"due_before,omitempty" jsonschema:"RFC3339 exclusive upper bound: due_time < due_before. tasks without a due date never match"`
	DueAfter  string   `json:"due_after,omitempty" jsonschema:"RFC3339 inclusive lower bound: due_after <= due_time. tasks without a due date never match"`
	HasDue    *bool    `json:"has_due,omitempty" jsonschema:"true for only tasks with a due date; false for only tasks without one; omit for both"`
	Source    *string  `json:"source,omitempty" jsonschema:"exact source match, e.g. github or ics:work; the empty string matches only local (non-synced) tasks; omit for any source"`
	Text      string   `json:"text,omitempty" jsonschema:"case-insensitive substring match over title and notes"`
	OrderBy   string   `json:"order_by,omitempty" jsonschema:"one of created, updated, due, title, completed, optionally followed by asc or desc (e.g. due asc); default created desc; with due or completed, tasks lacking that timestamp sort last"`
	Limit     int      `json:"limit,omitempty" jsonschema:"maximum tasks to return; default 100"`
}

type getTaskInput struct {
	ID string `json:"id" jsonschema:"the task id"`
}

type createTaskInput struct {
	Title      string   `json:"title" jsonschema:"the task title (required)"`
	Notes      string   `json:"notes,omitempty" jsonschema:"free-form notes"`
	Labels     []string `json:"labels,omitempty" jsonschema:"labels to attach, e.g. p1, project:home, agent:claude"`
	DueTime    string   `json:"due_time,omitempty" jsonschema:"due date as an RFC3339 timestamp, e.g. 2026-07-10T17:00:00Z"`
	Recurrence string   `json:"recurrence,omitempty" jsonschema:"repeat rule as natural language (every day, every weekday, every 2 weeks, mon,wed,fri) or a canonical RRULE subset; completing a recurring task rolls it forward instead of closing it"`
	ParentID   string   `json:"parent_id,omitempty" jsonschema:"id of an existing top-level task to nest this one under; hierarchy is one level deep"`
}

type updateTaskInput struct {
	ID               string    `json:"id" jsonschema:"the task id"`
	Title            *string   `json:"title,omitempty" jsonschema:"new title"`
	Notes            *string   `json:"notes,omitempty" jsonschema:"new notes; an empty string clears them"`
	Labels           *[]string `json:"labels,omitempty" jsonschema:"replacement for the ENTIRE label set; get_task first to add or remove one label"`
	DueTime          string    `json:"due_time,omitempty" jsonschema:"new due date (RFC3339); mutually exclusive with clear_due"`
	ClearDue         bool      `json:"clear_due,omitempty" jsonschema:"remove the due date"`
	Recurrence       *string   `json:"recurrence,omitempty" jsonschema:"new repeat rule (natural language or canonical RRULE subset); an empty string stops the task recurring; local tasks only"`
	ParentID         *string   `json:"parent_id,omitempty" jsonschema:"id of a top-level parent to nest under (one level deep); an empty string detaches to top-level"`
	ExpectedRevision int       `json:"expected_revision,omitempty" jsonschema:"revision from a previous read; the update fails instead of overwriting if the task has changed since"`
}

type taskIDInput struct {
	ID string `json:"id" jsonschema:"the task id"`
}

type listLabelsInput struct {
	IncludeCompleted bool `json:"include_completed,omitempty" jsonschema:"count completed tasks too; by default counts cover active tasks only"`
}

// --- handlers ---------------------------------------------------------------

func (b *bridge) listTasks(ctx context.Context, _ *mcp.CallToolRequest, in listTasksInput) (*mcp.CallToolResult, any, error) {
	filter := &taskpb.TaskFilter{
		LabelsAny: in.LabelsAny,
		LabelsAll: in.LabelsAll,
		Completed: in.Completed,
		HasDue:    in.HasDue,
		Source:    in.Source,
		Text:      in.Text,
	}
	if in.DueBefore != "" {
		ts, err := parseRFC3339("due_before", in.DueBefore)
		if err != nil {
			return nil, nil, err
		}
		filter.DueBefore = ts
	}
	if in.DueAfter != "" {
		ts, err := parseRFC3339("due_after", in.DueAfter)
		if err != nil {
			return nil, nil, err
		}
		filter.DueAfter = ts
	}

	limit := in.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	// Page through the daemon until the limit is filled or tasks run out;
	// the tool itself is unpaginated on purpose (agents want one result).
	var tasks []*taskpb.Task
	token := ""
	for len(tasks) < limit {
		res, err := b.tc.ListTasks(ctx, connect.NewRequest(&taskpb.ListTasksRequest{
			Filter:    filter,
			OrderBy:   in.OrderBy,
			PageSize:  int32(min(limit-len(tasks), 1000)),
			PageToken: token,
		}))
		if err != nil {
			return nil, nil, rpcErr(err)
		}
		tasks = append(tasks, res.Msg.GetTasks()...)
		token = res.Msg.GetNextPageToken()
		if token == "" {
			break
		}
	}
	if len(tasks) > limit {
		tasks = tasks[:limit]
	}
	out, err := tasksJSON(tasks)
	if err != nil {
		return nil, nil, err
	}
	return nil, out, nil
}

func (b *bridge) getTask(ctx context.Context, _ *mcp.CallToolRequest, in getTaskInput) (*mcp.CallToolResult, any, error) {
	res, err := b.tc.GetTask(ctx, connect.NewRequest(&taskpb.GetTaskRequest{Id: in.ID}))
	if err != nil {
		return nil, nil, rpcErr(err)
	}
	return taskResult(res.Msg.GetTask())
}

func (b *bridge) createTask(ctx context.Context, _ *mcp.CallToolRequest, in createTaskInput) (*mcp.CallToolResult, any, error) {
	req := &taskpb.CreateTaskRequest{
		Title:    in.Title,
		Notes:    in.Notes,
		Labels:   in.Labels,
		ParentId: in.ParentID,
	}
	if in.DueTime != "" {
		ts, err := parseRFC3339("due_time", in.DueTime)
		if err != nil {
			return nil, nil, err
		}
		req.DueTime = ts
	}
	if in.Recurrence != "" {
		rule, err := normalizeRecurrence(in.Recurrence)
		if err != nil {
			return nil, nil, err
		}
		req.Recurrence = rule
	}
	res, err := b.tc.CreateTask(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, nil, rpcErr(err)
	}
	return taskResult(res.Msg.GetTask())
}

func (b *bridge) updateTask(ctx context.Context, _ *mcp.CallToolRequest, in updateTaskInput) (*mcp.CallToolResult, any, error) {
	task := &taskpb.Task{}
	var paths []string
	if in.Title != nil {
		task.Title = *in.Title
		paths = append(paths, "title")
	}
	if in.Notes != nil {
		task.Notes = *in.Notes
		paths = append(paths, "notes")
	}
	if in.Labels != nil {
		task.Labels = *in.Labels
		paths = append(paths, "labels")
	}
	switch {
	case in.ClearDue && in.DueTime != "":
		return nil, nil, errors.New("provide only one of due_time or clear_due")
	case in.ClearDue:
		// Masking due_time while leaving it unset clears the due date.
		paths = append(paths, "due_time")
	case in.DueTime != "":
		ts, err := parseRFC3339("due_time", in.DueTime)
		if err != nil {
			return nil, nil, err
		}
		task.DueTime = ts
		paths = append(paths, "due_time")
	}
	if in.Recurrence != nil {
		if *in.Recurrence != "" {
			rule, err := normalizeRecurrence(*in.Recurrence)
			if err != nil {
				return nil, nil, err
			}
			task.Recurrence = rule
		}
		paths = append(paths, "recurrence") // masked but empty stops recurring
	}
	if in.ParentID != nil {
		task.ParentId = *in.ParentID
		paths = append(paths, "parent_id") // masked but empty detaches to top-level
	}
	if len(paths) == 0 {
		return nil, nil, errors.New("nothing to update: provide at least one of title, notes, labels, due_time, clear_due, recurrence, or parent_id")
	}
	if in.ExpectedRevision < 0 {
		return nil, nil, fmt.Errorf("expected_revision must not be negative, got %d", in.ExpectedRevision)
	}

	res, err := b.tc.UpdateTask(ctx, connect.NewRequest(&taskpb.UpdateTaskRequest{
		Id:               in.ID,
		UpdateMask:       &fieldmaskpb.FieldMask{Paths: paths},
		Task:             task,
		ExpectedRevision: uint64(in.ExpectedRevision),
	}))
	if err != nil {
		var cerr *connect.Error
		if errors.As(err, &cerr) && cerr.Code() == connect.CodeAborted {
			return nil, nil, fmt.Errorf(
				"conflict: %s — the task changed since it was last read (stale expected_revision); call get_task for its current state and revision, then retry the update",
				cerr.Message())
		}
		return nil, nil, rpcErr(err)
	}
	return taskResult(res.Msg.GetTask())
}

func (b *bridge) completeTask(ctx context.Context, _ *mcp.CallToolRequest, in taskIDInput) (*mcp.CallToolResult, any, error) {
	return b.setCompleted(ctx, in.ID, timestamppb.Now())
}

func (b *bridge) reopenTask(ctx context.Context, _ *mcp.CallToolRequest, in taskIDInput) (*mcp.CallToolResult, any, error) {
	return b.setCompleted(ctx, in.ID, nil)
}

// setCompleted is the shared body of complete_task and reopen_task:
// completing IS setting completed_time, and masking it while unset re-opens.
func (b *bridge) setCompleted(ctx context.Context, id string, completed *timestamppb.Timestamp) (*mcp.CallToolResult, any, error) {
	res, err := b.tc.UpdateTask(ctx, connect.NewRequest(&taskpb.UpdateTaskRequest{
		Id:         id,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"completed_time"}},
		Task:       &taskpb.Task{CompletedTime: completed},
	}))
	if err != nil {
		return nil, nil, rpcErr(err)
	}
	return taskResult(res.Msg.GetTask())
}

func (b *bridge) deleteTask(ctx context.Context, _ *mcp.CallToolRequest, in taskIDInput) (*mcp.CallToolResult, any, error) {
	// Guard before any RPC: a refusal must never reach the daemon.
	if err := guardDelete(); err != nil {
		return nil, nil, err
	}
	if _, err := b.tc.DeleteTask(ctx, connect.NewRequest(&taskpb.DeleteTaskRequest{Id: in.ID})); err != nil {
		return nil, nil, rpcErr(err)
	}
	return nil, map[string]any{"id": in.ID, "deleted": true}, nil
}

func (b *bridge) listLabels(ctx context.Context, _ *mcp.CallToolRequest, in listLabelsInput) (*mcp.CallToolResult, any, error) {
	res, err := b.tc.ListLabels(ctx, connect.NewRequest(&taskpb.ListLabelsRequest{
		IncludeCompleted: in.IncludeCompleted,
	}))
	if err != nil {
		return nil, nil, rpcErr(err)
	}
	type labelCount struct {
		Label string `json:"label"`
		Count int64  `json:"count"`
	}
	counts := make([]labelCount, 0, len(res.Msg.GetLabels()))
	for _, lc := range res.Msg.GetLabels() {
		counts = append(counts, labelCount{Label: lc.GetLabel(), Count: lc.GetCount()})
	}
	return nil, map[string]any{"labels": counts}, nil
}

// --- helpers ----------------------------------------------------------------

// normalizeRecurrence accepts either natural language ("every weekday") or an
// already-canonical RRULE subset ("FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR") and
// returns the canonical form the server stores.
func normalizeRecurrence(s string) (string, error) {
	if rule, err := recur.FromNatural(s); err == nil {
		return rule, nil
	}
	if _, err := recur.Parse(s); err != nil {
		return "", fmt.Errorf("recurrence %q: not natural language or a supported RRULE subset (%v)", s, err)
	}
	return s, nil
}

// parseRFC3339 parses a timestamp input. This frontend deliberately accepts
// only RFC3339 — agents pass structured data, and one unambiguous format
// keeps behavior predictable.
func parseRFC3339(field, s string) (*timestamppb.Timestamp, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid RFC3339 timestamp %q (want e.g. 2026-07-10T17:00:00Z)", field, s)
	}
	return timestamppb.New(t), nil
}

// taskResult renders one task as protojson structured content. protojson
// keeps every timestamp an RFC3339 string, matching the tool inputs.
func taskResult(t *taskpb.Task) (*mcp.CallToolResult, any, error) {
	raw, err := protojson.Marshal(t)
	if err != nil {
		return nil, nil, err
	}
	return nil, json.RawMessage(raw), nil
}

// tasksJSON wraps tasks in {"tasks": [...]} — structured tool content must
// be a JSON object, not a bare array.
func tasksJSON(tasks []*taskpb.Task) (any, error) {
	items := make([]json.RawMessage, len(tasks))
	for i, t := range tasks {
		raw, err := protojson.Marshal(t)
		if err != nil {
			return nil, err
		}
		items[i] = raw
	}
	return map[string]any{"tasks": items}, nil
}

// rpcErr unwraps a connect error into a compact "code: message" tool error,
// so agents see the daemon's own diagnostic rather than transport framing.
func rpcErr(err error) error {
	var cerr *connect.Error
	if errors.As(err, &cerr) {
		return fmt.Errorf("%s: %s", cerr.Code(), cerr.Message())
	}
	return err
}
