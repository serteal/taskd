package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

// registerTools wires every tool to its typed handler. AddTool infers the
// JSON Schema for each input struct (see the jsonschema tags), so agents get
// self-describing tools; results are protojson so agents get structured data,
// not tables.
func (b *bridge) registerTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "query_items", Description: queryItemsDesc}, b.queryItems)
	mcp.AddTool(s, &mcp.Tool{Name: "get_item", Description: "Fetch one item by id (a unique id prefix resolves). Returns the full item as protojson."}, b.getItem)
	mcp.AddTool(s, &mcp.Tool{Name: "create_task", Description: "Create a native task (kind \"task\", the user's own todo). Returns the created item as protojson."}, b.createTask)
	mcp.AddTool(s, &mcp.Tool{Name: "update_todo", Description: updateTodoDesc}, b.updateTodo)
	mcp.AddTool(s, &mcp.Tool{Name: "complete_task", Description: "Mark an item completed (stamps completed_at). Optional reason is a free-form qualifier such as \"wontdo\". Returns the item as protojson."}, b.completeTask)
	mcp.AddTool(s, &mcp.Tool{Name: "reopen_task", Description: "Reopen a completed item (clears completed_at and the reason). Returns the item as protojson."}, b.reopenTask)
	mcp.AddTool(s, &mcp.Tool{Name: "invoke_intent", Description: invokeIntentDesc}, b.invokeIntent)
	mcp.AddTool(s, &mcp.Tool{Name: "list_pending", Description: "List outbox intent records (writes headed for remotes). By default only the live outbox (QUEUED, INFLIGHT, FAILED); set all=true to include terminal (CONFIRMED, DISCARDED) records. Returns records as protojson."}, b.listPending)
	mcp.AddTool(s, &mcp.Tool{Name: "list_rules", Description: "List automation rules in evaluation order, as protojson. Read-only introspection."}, b.listRules)
	mcp.AddTool(s, &mcp.Tool{Name: "list_views", Description: "List saved views (name, filter, description, order_by), as protojson. Read-only introspection."}, b.listViews)
	mcp.AddTool(s, &mcp.Tool{Name: "list_kinds", Description: "List item kinds and their facet bindings (the queryable shape), as protojson. Read-only introspection."}, b.listKinds)
}

const queryItemsDesc = `Query items with a CEL filter; returns matching items as protojson, one JSON item per line.

The filter is a CEL boolean expression evaluated per item. Variables available:
  completed  (bool)      todo completed?
  labels     (list)      todo labels
  project    (string)    todo project path, e.g. "work/reviews"
  kind       (string)    "task" for native items, else a plugin kind
  state      (string)    raw mirror state for tracked items
  due        (timestamp) effective due date
  has_due    (bool)      whether an effective due date exists
  snoozed    (bool)      currently snoozed?
  now        (timestamp) current time
  item.*                 the raw Item message, e.g. item.todo.note

Examples:
  !completed && "urgent" in labels
  kind == "calendar.event" && has_due && due < now + duration("24h")
  completed && project == "work/reviews"

order_by: a column optionally followed by asc/desc (due, created_at, updated_at, project, kind); default "created_at desc".
limit: max items to return; default 50, capped at 200.`

const updateTodoDesc = `Update the user-owned (todo) fields of an item, building a field mask from the fields you set — only present fields are written. Mirror/remote fields are not editable here (use invoke_intent). Returns the item as protojson.`

const invokeIntentDesc = `Invoke a semantic intent against an item's connector — the write-through path toward the remote system. Standard intents: rename, add_comment, set_due, set_start, assign, set_priority, set_completed, delete.

GUARDRAIL (DESIGN §14): agents have no confirm dialog, so destructive intents (delete) are refused unless the user opted in via the TASKMCP_ALLOW_DESTRUCTIVE env allowlist. Content intents pass through.

Returns the IntentRecord as protojson; inspect its "state" (QUEUED / INFLIGHT / CONFIRMED / FAILED) and "lastError" to know whether the remote confirmed, the outbox took over, or delivery failed.`

// --- input types -----------------------------------------------------------

// noInput is the empty argument type for read-only introspection tools.
type noInput struct{}

type queryItemsInput struct {
	Filter  string `json:"filter,omitempty" jsonschema:"CEL boolean filter over items; empty matches everything. e.g. !completed && \"urgent\" in labels"`
	OrderBy string `json:"order_by,omitempty" jsonschema:"column optionally followed by asc/desc: due, created_at, updated_at, project, kind. default created_at desc"`
	Limit   int    `json:"limit,omitempty" jsonschema:"max items to return; default 50, capped at 200"`
}

type getItemInput struct {
	ID string `json:"id" jsonschema:"item id; a unique prefix resolves"`
}

type createTaskInput struct {
	Title      string   `json:"title" jsonschema:"the task title (required)"`
	Project    string   `json:"project,omitempty" jsonschema:"path-style project, e.g. work/reviews"`
	Labels     []string `json:"labels,omitempty" jsonschema:"labels to attach"`
	Note       string   `json:"note,omitempty" jsonschema:"free-form note body"`
	DueRFC3339 string   `json:"due_rfc3339,omitempty" jsonschema:"due date as an RFC3339 timestamp, e.g. 2026-07-03T17:00:00Z"`
}

// updateTodoSet carries the fields to change. Pointers distinguish "field
// present" (write it, even to a zero value like an empty project) from
// "absent" (leave it untouched).
type updateTodoSet struct {
	Title         *string   `json:"title,omitempty" jsonschema:"new display title (todo.title_override)"`
	Project       *string   `json:"project,omitempty" jsonschema:"new project path; empty string clears it"`
	Note          *string   `json:"note,omitempty" jsonschema:"new note body"`
	Labels        *[]string `json:"labels,omitempty" jsonschema:"replacement label set (replaces all labels)"`
	DueRFC3339    string    `json:"due_rfc3339,omitempty" jsonschema:"new due date (RFC3339); mutually exclusive with clear_due"`
	ClearDue      bool      `json:"clear_due,omitempty" jsonschema:"clear the due date"`
	SnoozeRFC3339 string    `json:"snooze_rfc3339,omitempty" jsonschema:"snooze until (RFC3339); mutually exclusive with clear_snooze"`
	ClearSnooze   bool      `json:"clear_snooze,omitempty" jsonschema:"clear the snooze"`
}

type updateTodoInput struct {
	ID  string        `json:"id" jsonschema:"item id; a unique prefix resolves"`
	Set updateTodoSet `json:"set" jsonschema:"fields to change; only present fields are written"`
}

type completeTaskInput struct {
	ID     string `json:"id" jsonschema:"item id; a unique prefix resolves"`
	Reason string `json:"reason,omitempty" jsonschema:"optional completion qualifier, e.g. wontdo"`
}

type reopenTaskInput struct {
	ID string `json:"id" jsonschema:"item id; a unique prefix resolves"`
}

type invokeIntentInput struct {
	ItemID string         `json:"item_id" jsonschema:"item id; a unique prefix resolves"`
	Intent string         `json:"intent" jsonschema:"lowercase intent name: rename, add_comment, set_due, set_start, assign, set_priority, set_completed, delete"`
	Params map[string]any `json:"params,omitempty" jsonschema:"intent parameters, e.g. {\"title\": \"New title\"} for rename"`
}

type listPendingInput struct {
	All bool `json:"all,omitempty" jsonschema:"include terminal (CONFIRMED, DISCARDED) records too; default only the live outbox"`
}

// --- handlers ---------------------------------------------------------------

func (b *bridge) queryItems(ctx context.Context, _ *mcp.CallToolRequest, in queryItemsInput) (*mcp.CallToolResult, any, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	resp, err := b.cl.Items().QueryItems(ctx, &taskcorev1.QueryItemsRequest{
		Filter:   in.Filter,
		OrderBy:  in.OrderBy,
		PageSize: int32(limit),
	})
	if err != nil {
		return nil, nil, rpcErr(err)
	}
	return jsonLinesResult(resp.GetItems())
}

func (b *bridge) getItem(ctx context.Context, _ *mcp.CallToolRequest, in getItemInput) (*mcp.CallToolResult, any, error) {
	resp, err := b.cl.Items().GetItem(ctx, &taskcorev1.GetItemRequest{Id: in.ID})
	if err != nil {
		return nil, nil, rpcErr(err)
	}
	return jsonLinesResult([]*taskcorev1.Item{resp.GetItem()})
}

func (b *bridge) createTask(ctx context.Context, _ *mcp.CallToolRequest, in createTaskInput) (*mcp.CallToolResult, any, error) {
	todo := &taskcorev1.Todo{
		TitleOverride: in.Title,
		Project:       in.Project,
		Labels:        in.Labels,
		Note:          in.Note,
	}
	if in.DueRFC3339 != "" {
		t, err := parseRFC3339(in.DueRFC3339)
		if err != nil {
			return nil, nil, err
		}
		todo.Due = timestamppb.New(t)
	}
	resp, err := b.cl.Items().CreateItem(ctx, &taskcorev1.CreateItemRequest{Todo: todo})
	if err != nil {
		return nil, nil, rpcErr(err)
	}
	return jsonLinesResult([]*taskcorev1.Item{resp.GetItem()})
}

func (b *bridge) updateTodo(ctx context.Context, _ *mcp.CallToolRequest, in updateTodoInput) (*mcp.CallToolResult, any, error) {
	todo := &taskcorev1.Todo{}
	var paths []string
	set := in.Set

	if set.Title != nil {
		todo.TitleOverride = *set.Title
		paths = append(paths, "todo.title_override")
	}
	if set.Project != nil {
		todo.Project = *set.Project
		paths = append(paths, "todo.project")
	}
	if set.Note != nil {
		todo.Note = *set.Note
		paths = append(paths, "todo.note")
	}
	if set.Labels != nil {
		todo.Labels = *set.Labels
		paths = append(paths, "todo.labels")
	}
	switch {
	case set.ClearDue && set.DueRFC3339 != "":
		return nil, nil, errors.New("set: provide only one of due_rfc3339 or clear_due")
	case set.ClearDue:
		paths = append(paths, "todo.due") // leaving todo.Due nil clears it
	case set.DueRFC3339 != "":
		t, err := parseRFC3339(set.DueRFC3339)
		if err != nil {
			return nil, nil, err
		}
		todo.Due = timestamppb.New(t)
		paths = append(paths, "todo.due")
	}
	switch {
	case set.ClearSnooze && set.SnoozeRFC3339 != "":
		return nil, nil, errors.New("set: provide only one of snooze_rfc3339 or clear_snooze")
	case set.ClearSnooze:
		paths = append(paths, "todo.snoozed_until")
	case set.SnoozeRFC3339 != "":
		t, err := parseRFC3339(set.SnoozeRFC3339)
		if err != nil {
			return nil, nil, err
		}
		todo.SnoozedUntil = timestamppb.New(t)
		paths = append(paths, "todo.snoozed_until")
	}
	if len(paths) == 0 {
		return nil, nil, errors.New("set: at least one field must be provided")
	}

	resp, err := b.cl.Items().UpdateItem(ctx, &taskcorev1.UpdateItemRequest{
		Id:         in.ID,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: paths},
		Item:       &taskcorev1.Item{Todo: todo},
	})
	if err != nil {
		return nil, nil, rpcErr(err)
	}
	return jsonLinesResult([]*taskcorev1.Item{resp.GetItem()})
}

func (b *bridge) completeTask(ctx context.Context, _ *mcp.CallToolRequest, in completeTaskInput) (*mcp.CallToolResult, any, error) {
	todo := &taskcorev1.Todo{Completed: true}
	paths := []string{"todo.completed"}
	if in.Reason != "" {
		todo.CompletedReason = in.Reason
		paths = append(paths, "todo.completed_reason")
	}
	resp, err := b.cl.Items().UpdateItem(ctx, &taskcorev1.UpdateItemRequest{
		Id:         in.ID,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: paths},
		Item:       &taskcorev1.Item{Todo: todo},
	})
	if err != nil {
		return nil, nil, rpcErr(err)
	}
	return jsonLinesResult([]*taskcorev1.Item{resp.GetItem()})
}

func (b *bridge) reopenTask(ctx context.Context, _ *mcp.CallToolRequest, in reopenTaskInput) (*mcp.CallToolResult, any, error) {
	resp, err := b.cl.Items().UpdateItem(ctx, &taskcorev1.UpdateItemRequest{
		Id:         in.ID,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"todo.completed"}},
		Item:       &taskcorev1.Item{Todo: &taskcorev1.Todo{Completed: false}},
	})
	if err != nil {
		return nil, nil, rpcErr(err)
	}
	return jsonLinesResult([]*taskcorev1.Item{resp.GetItem()})
}

func (b *bridge) invokeIntent(ctx context.Context, _ *mcp.CallToolRequest, in invokeIntentInput) (*mcp.CallToolResult, any, error) {
	// Guard client-side, before any RPC: a refusal must never reach the daemon.
	if err := guardIntent(in.Intent); err != nil {
		return nil, nil, err
	}
	var params *structpb.Struct
	if len(in.Params) > 0 {
		s, err := structpb.NewStruct(in.Params)
		if err != nil {
			return nil, nil, fmt.Errorf("params: %v", err)
		}
		params = s
	}
	resp, err := b.intents.InvokeIntent(ctx, &taskcorev1.InvokeIntentRequest{
		ItemId: in.ItemID,
		Intent: in.Intent,
		Params: params,
	})
	if err != nil {
		return nil, nil, rpcErr(err)
	}
	return jsonLinesResult([]*taskcorev1.IntentRecord{resp.GetRecord()})
}

func (b *bridge) listPending(ctx context.Context, _ *mcp.CallToolRequest, in listPendingInput) (*mcp.CallToolResult, any, error) {
	req := &taskcorev1.ListIntentsRequest{}
	if in.All {
		req.States = allIntentStates
	}
	resp, err := b.intents.ListIntents(ctx, req)
	if err != nil {
		return nil, nil, rpcErr(err)
	}
	return jsonLinesResult(resp.GetRecords())
}

func (b *bridge) listRules(ctx context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, any, error) {
	resp, err := b.rules.ListRules(ctx, &taskcorev1.ListRulesRequest{})
	if err != nil {
		return nil, nil, rpcErr(err)
	}
	return jsonLinesResult(resp.GetRules())
}

func (b *bridge) listViews(ctx context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, any, error) {
	resp, err := b.cl.Views().ListViews(ctx, &taskcorev1.ListViewsRequest{})
	if err != nil {
		return nil, nil, rpcErr(err)
	}
	return jsonLinesResult(resp.GetViews())
}

func (b *bridge) listKinds(ctx context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, any, error) {
	resp, err := b.cl.Schema().ListKinds(ctx, &taskcorev1.ListKindsRequest{})
	if err != nil {
		return nil, nil, rpcErr(err)
	}
	return jsonLinesResult(resp.GetKinds())
}

// allIntentStates enumerates every IntentState, used by list_pending(all) to
// escape the default "live outbox only" behavior of an empty states filter.
var allIntentStates = []taskcorev1.IntentState{
	taskcorev1.IntentState_INTENT_STATE_QUEUED,
	taskcorev1.IntentState_INTENT_STATE_INFLIGHT,
	taskcorev1.IntentState_INTENT_STATE_CONFIRMED,
	taskcorev1.IntentState_INTENT_STATE_FAILED,
	taskcorev1.IntentState_INTENT_STATE_DISCARDED,
}

// --- helpers ----------------------------------------------------------------

// parseRFC3339 parses an RFC3339 instant. The tool inputs name the format
// explicitly (due_rfc3339, snooze_rfc3339); this frontend deliberately accepts
// only RFC3339 (not the CLI's looser WHEN grammar) because agents pass
// structured data and an unambiguous format keeps behavior predictable.
func parseRFC3339(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid RFC3339 timestamp %q (want e.g. 2026-07-03T17:00:00Z): %v", s, err)
	}
	return t, nil
}

// jsonLinesResult renders proto messages as protojson, one per line, into a
// tool result — the structured shape agents consume.
func jsonLinesResult[T proto.Message](msgs []T) (*mcp.CallToolResult, any, error) {
	txt, err := jsonLines(msgs)
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: txt}}}, nil, nil
}

func jsonLines[T proto.Message](msgs []T) (string, error) {
	var b strings.Builder
	for i, m := range msgs {
		out, err := protojson.Marshal(m)
		if err != nil {
			return "", err
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		b.Write(out)
	}
	return b.String(), nil
}

// rpcErr unwraps a gRPC status into a compact "Code: message" tool error, so
// agents see the daemon's own diagnostic rather than gRPC transport framing.
func rpcErr(err error) error {
	if st, ok := status.FromError(err); ok {
		return fmt.Errorf("%s: %s", st.Code(), st.Message())
	}
	return err
}
