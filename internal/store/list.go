package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	taskpb "todoapp/gen/task"
)

const (
	defaultPageSize = 100
	maxPageSize     = 1000
)

// Page describes one ListTasks request.
type Page struct {
	Filter    *taskpb.TaskFilter // may be nil
	OrderBy   string             // raw request string; "" = "created desc"
	PageSize  int32              // <=0 → 100; capped at 1000
	PageToken string
}

// sortKey is one component of a total order: a SQL expression, its direction,
// and how to reproduce its value from a row for page tokens.
type sortKey struct {
	expr   string
	desc   bool
	isText bool // token value is a string (vs int64)
	value  func(*taskRow) any
}

// orderSpec is a parsed, normalized order_by. name is the canonical
// "field dir" form embedded in page tokens; keys always end with id so the
// order is total and pagination stable.
type orderSpec struct {
	name string
	keys []sortKey
}

func parseOrderBy(orderBy string) (*orderSpec, error) {
	parts := strings.Fields(orderBy)
	var field string
	var desc bool
	switch len(parts) {
	case 0:
		field, desc = "created", true
	case 1:
		field = parts[0]
	case 2:
		field = parts[0]
		switch parts[1] {
		case "asc":
		case "desc":
			desc = true
		default:
			return nil, fmt.Errorf("order_by direction %q: %w", parts[1], ErrInvalid)
		}
	default:
		return nil, fmt.Errorf("order_by %q: %w", orderBy, ErrInvalid)
	}

	var keys []sortKey
	switch field {
	case "created":
		keys = []sortKey{{expr: "created_ms", desc: desc, value: func(r *taskRow) any { return r.createdMs }}}
	case "updated":
		keys = []sortKey{{expr: "updated_ms", desc: desc, value: func(r *taskRow) any { return r.updatedMs }}}
	case "title":
		keys = []sortKey{{expr: "title", desc: desc, isText: true, value: func(r *taskRow) any { return r.title }}}
	case "due":
		// Tasks without a due date sort last regardless of direction: the
		// null flag is always ascending, ahead of the due value itself.
		keys = []sortKey{
			{expr: "(due_ms IS NULL)", value: func(r *taskRow) any {
				if r.dueMs.Valid {
					return int64(0)
				}
				return int64(1)
			}},
			{expr: "COALESCE(due_ms, 0)", desc: desc, value: func(r *taskRow) any { return r.dueMs.Int64 }},
		}
	default:
		return nil, fmt.Errorf("order_by field %q: %w", field, ErrInvalid)
	}
	keys = append(keys, sortKey{expr: "id", desc: desc, isText: true, value: func(r *taskRow) any { return r.id }})

	name := field + " asc"
	if desc {
		name = field + " desc"
	}
	return &orderSpec{name: name, keys: keys}, nil
}

func (o *orderSpec) clause() string {
	parts := make([]string, len(o.keys))
	for i, k := range o.keys {
		dir := " ASC"
		if k.desc {
			dir = " DESC"
		}
		parts[i] = k.expr + dir
	}
	return strings.Join(parts, ", ")
}

// likeEscaper makes a needle literal inside a LIKE pattern (ESCAPE '\').
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// compileFilter renders a TaskFilter as AND-ed SQL conditions. A nil or empty
// filter yields none.
func compileFilter(f *taskpb.TaskFilter) (conds []string, args []any, err error) {
	if f == nil {
		return nil, nil, nil
	}
	if len(f.GetLabelsAny()) > 0 {
		labels, err := normalizeLabels(f.GetLabelsAny())
		if err != nil {
			return nil, nil, fmt.Errorf("labels_any: %w", err)
		}
		ph := strings.TrimSuffix(strings.Repeat("?,", len(labels)), ",")
		conds = append(conds, "EXISTS (SELECT 1 FROM task_labels WHERE task_id = tasks.id AND label IN ("+ph+"))")
		for _, l := range labels {
			args = append(args, l)
		}
	}
	if len(f.GetLabelsAll()) > 0 {
		labels, err := normalizeLabels(f.GetLabelsAll())
		if err != nil {
			return nil, nil, fmt.Errorf("labels_all: %w", err)
		}
		for _, l := range labels {
			conds = append(conds, "EXISTS (SELECT 1 FROM task_labels WHERE task_id = tasks.id AND label = ?)")
			args = append(args, l)
		}
	}
	if f.Completed != nil {
		if *f.Completed {
			conds = append(conds, "completed_ms IS NOT NULL")
		} else {
			conds = append(conds, "completed_ms IS NULL")
		}
	}
	// Half-open range [due_after, due_before); tasks without a due date never
	// match a bound.
	if after := f.GetDueAfter(); after != nil {
		conds = append(conds, "due_ms IS NOT NULL AND due_ms >= ?")
		args = append(args, after.AsTime().UnixMilli())
	}
	if before := f.GetDueBefore(); before != nil {
		conds = append(conds, "due_ms IS NOT NULL AND due_ms < ?")
		args = append(args, before.AsTime().UnixMilli())
	}
	if f.HasDue != nil {
		if *f.HasDue {
			conds = append(conds, "due_ms IS NOT NULL")
		} else {
			conds = append(conds, "due_ms IS NULL")
		}
	}
	// proto3 optional: the filter applies whenever set, even to "".
	if f.Source != nil {
		conds = append(conds, "source = ?")
		args = append(args, *f.Source)
	}
	if text := f.GetText(); text != "" {
		// SQLite LIKE is only ASCII-case-insensitive; accepted for this store.
		needle := likeEscaper.Replace(text)
		conds = append(conds, `(title LIKE '%' || ? || '%' ESCAPE '\' OR notes LIKE '%' || ? || '%' ESCAPE '\')`)
		args = append(args, needle, needle)
	}
	return conds, args, nil
}

// pageToken is the JSON payload of a next_page_token: the normalized order it
// was issued under and the sort-key values of the last returned row.
type pageToken struct {
	Order string `json:"o"`
	Keys  []any  `json:"k"`
}

func encodePageToken(spec *orderSpec, last *taskRow) (string, error) {
	tok := pageToken{Order: spec.name, Keys: make([]any, len(spec.keys))}
	for i, k := range spec.keys {
		tok.Keys[i] = k.value(last)
	}
	b, err := json.Marshal(tok)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func decodePageToken(spec *orderSpec, token string) ([]any, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("malformed page_token: %w", ErrInvalid)
	}
	var tok pageToken
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&tok); err != nil {
		return nil, fmt.Errorf("malformed page_token: %w", ErrInvalid)
	}
	if tok.Order != spec.name {
		return nil, fmt.Errorf("page_token was issued for order %q, request orders by %q: %w",
			tok.Order, spec.name, ErrInvalid)
	}
	if len(tok.Keys) != len(spec.keys) {
		return nil, fmt.Errorf("malformed page_token: %w", ErrInvalid)
	}
	vals := make([]any, len(spec.keys))
	for i, k := range spec.keys {
		if k.isText {
			s, ok := tok.Keys[i].(string)
			if !ok {
				return nil, fmt.Errorf("malformed page_token: %w", ErrInvalid)
			}
			vals[i] = s
			continue
		}
		n, ok := tok.Keys[i].(json.Number)
		if !ok {
			return nil, fmt.Errorf("malformed page_token: %w", ErrInvalid)
		}
		v, err := strconv.ParseInt(n.String(), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("malformed page_token: %w", ErrInvalid)
		}
		vals[i] = v
	}
	return vals, nil
}

// keysetPredicate renders "strictly after the row with these key values" as
// the general OR-expansion, honoring each key's own direction:
//
//	(k1 c1 ?) OR (k1 = ? AND k2 c2 ?) OR (k1 = ? AND k2 = ? AND k3 c3 ?)
//
// where cN is > for an ascending key and < for a descending one.
func keysetPredicate(keys []sortKey, vals []any) (string, []any) {
	terms := make([]string, len(keys))
	var args []any
	for i, key := range keys {
		var parts []string
		for j := 0; j < i; j++ {
			parts = append(parts, keys[j].expr+" = ?")
			args = append(args, vals[j])
		}
		cmp := " > ?"
		if key.desc {
			cmp = " < ?"
		}
		parts = append(parts, key.expr+cmp)
		args = append(args, vals[i])
		terms[i] = "(" + strings.Join(parts, " AND ") + ")"
	}
	return "(" + strings.Join(terms, " OR ") + ")", args
}

// List returns one page of tasks matching p.Filter in p.OrderBy order, and a
// token for the next page ("" on the last page). Tokens are only valid for
// the order they were issued under.
func (s *Store) List(ctx context.Context, p Page) ([]*taskpb.Task, string, error) {
	spec, err := parseOrderBy(p.OrderBy)
	if err != nil {
		return nil, "", err
	}
	size := int(p.PageSize)
	if size <= 0 {
		size = defaultPageSize
	}
	if size > maxPageSize {
		size = maxPageSize
	}
	conds, args, err := compileFilter(p.Filter)
	if err != nil {
		return nil, "", err
	}
	if p.PageToken != "" {
		vals, err := decodePageToken(spec, p.PageToken)
		if err != nil {
			return nil, "", err
		}
		pred, predArgs := keysetPredicate(spec.keys, vals)
		conds = append(conds, pred)
		args = append(args, predArgs...)
	}

	query := "SELECT " + taskColumns + " FROM tasks"
	if len(conds) > 0 {
		query += " WHERE " + strings.Join(conds, " AND ")
	}
	// Fetch one extra row to learn whether a next page exists.
	query += " ORDER BY " + spec.clause() + " LIMIT ?"
	args = append(args, size+1)

	taskRows, err := s.queryTasks(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	hasMore := len(taskRows) > size
	if hasMore {
		taskRows = taskRows[:size]
	}

	labels, err := s.labelsForTasks(ctx, taskRows)
	if err != nil {
		return nil, "", err
	}
	tasks := make([]*taskpb.Task, len(taskRows))
	for i, r := range taskRows {
		if tasks[i], err = r.proto(labels[r.id]); err != nil {
			return nil, "", err
		}
	}
	if !hasMore {
		return tasks, "", nil
	}
	next, err := encodePageToken(spec, taskRows[len(taskRows)-1])
	if err != nil {
		return nil, "", err
	}
	return tasks, next, nil
}

// queryTasks runs a task query and drains it fully (the single-connection
// pool requires no result set be left open across statements).
func (s *Store) queryTasks(ctx context.Context, query string, args ...any) ([]*taskRow, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()
	var out []*taskRow
	for rows.Next() {
		var r taskRow
		if err := r.scan(rows); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

func (s *Store) labelsForTasks(ctx context.Context, taskRows []*taskRow) (map[string][]string, error) {
	if len(taskRows) == 0 {
		return nil, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(taskRows)), ",")
	args := make([]any, len(taskRows))
	for i, r := range taskRows {
		args[i] = r.id
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT task_id, label FROM task_labels WHERE task_id IN ("+ph+") ORDER BY label", args...)
	if err != nil {
		return nil, fmt.Errorf("load labels: %w", err)
	}
	defer rows.Close()
	out := make(map[string][]string, len(taskRows))
	for rows.Next() {
		var id, label string
		if err := rows.Scan(&id, &label); err != nil {
			return nil, err
		}
		out[id] = append(out[id], label)
	}
	return out, rows.Err()
}

// ListLabels returns the distinct labels in use with task counts, sorted by
// label. By default only active tasks count; includeCompleted counts all.
func (s *Store) ListLabels(ctx context.Context, includeCompleted bool) ([]*taskpb.LabelCount, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT label, COUNT(*) FROM task_labels JOIN tasks ON tasks.id = task_id
		 WHERE (? OR tasks.completed_ms IS NULL) GROUP BY label ORDER BY label`,
		includeCompleted)
	if err != nil {
		return nil, fmt.Errorf("list labels: %w", err)
	}
	defer rows.Close()
	var out []*taskpb.LabelCount
	for rows.Next() {
		lc := &taskpb.LabelCount{}
		if err := rows.Scan(&lc.Label, &lc.Count); err != nil {
			return nil, err
		}
		out = append(out, lc)
	}
	return out, rows.Err()
}
