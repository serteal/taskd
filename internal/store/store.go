// Package store implements taskd's persistence layer: a single-file SQLite
// database holding tasks and their labels, exposed as CRUD, filtered
// keyset-paginated listing, and batch reconciliation of externally synced
// tasks (UpsertExternal).
//
// Two invariants hold for every task the store returns:
//
//   - Timestamps are truncated to millisecond precision on write; reads
//     reproduce the stored millisecond value exactly.
//   - Labels are whitespace-trimmed, deduplicated, and sorted ascending.
//
// All errors that reflect caller mistakes wrap one of the sentinel errors
// ErrNotFound, ErrRevisionMismatch, or ErrInvalid, testable with errors.Is.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	_ "modernc.org/sqlite"

	taskpb "todoapp/gen/task"
)

var (
	// ErrNotFound reports that no task has the requested id.
	ErrNotFound = errors.New("task not found")
	// ErrRevisionMismatch reports that an optimistic-concurrency check failed.
	ErrRevisionMismatch = errors.New("revision mismatch")
	// ErrInvalid reports an invalid argument; errors carry detail via wrapping.
	ErrInvalid = errors.New("invalid argument")
)

// Store is a task store backed by a SQLite database file. It is safe for
// concurrent use; writes serialize on a single connection.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

var schema = []string{
	`CREATE TABLE IF NOT EXISTS tasks (
		id             TEXT PRIMARY KEY,
		title          TEXT NOT NULL,
		notes          TEXT NOT NULL DEFAULT '',
		due_ms         INTEGER,
		completed_ms   INTEGER,
		source         TEXT NOT NULL DEFAULT '',
		external_ref   TEXT NOT NULL DEFAULT '',
		external_data  TEXT,
		revision       INTEGER NOT NULL,
		created_ms     INTEGER NOT NULL,
		updated_ms     INTEGER NOT NULL
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS tasks_by_external ON tasks(source, external_ref) WHERE source <> ''`,
	`CREATE INDEX IF NOT EXISTS tasks_by_completed_due ON tasks(completed_ms, due_ms)`,
	`CREATE INDEX IF NOT EXISTS tasks_by_created ON tasks(created_ms, id)`,
	`CREATE INDEX IF NOT EXISTS tasks_by_updated ON tasks(updated_ms, id)`,
	`CREATE TABLE IF NOT EXISTS task_labels (
		task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
		label   TEXT NOT NULL,
		PRIMARY KEY (task_id, label)
	) WITHOUT ROWID`,
	`CREATE INDEX IF NOT EXISTS labels_by_label ON task_labels(label)`,
}

// Open opens (creating if necessary) the database at path. now supplies the
// clock used for create/update timestamps; nil means time.Now.
func Open(ctx context.Context, path string, now func() time.Time) (*Store, error) {
	if now == nil {
		now = time.Now
	}
	// _pragma DSN parameters apply to every connection the pool opens.
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// One connection is plenty at personal scale and rules out SQLITE_BUSY.
	// It also means a query issued while a *sql.Rows is open would deadlock,
	// so every method drains result sets before issuing the next statement.
	db.SetMaxOpenConns(1)
	for _, stmt := range schema {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("apply schema: %w", err)
		}
	}
	return &Store{db: db, now: now}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}

// querier is the subset of *sql.DB and *sql.Tx the store's helpers need.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const taskColumns = "id, title, notes, due_ms, completed_ms, source, external_ref, external_data, revision, created_ms, updated_ms"

// taskRow mirrors one tasks row; labels live in task_labels.
type taskRow struct {
	id           string
	title        string
	notes        string
	dueMs        sql.NullInt64
	completedMs  sql.NullInt64
	source       string
	externalRef  string
	externalData sql.NullString
	revision     int64
	createdMs    int64
	updatedMs    int64
}

func (r *taskRow) scan(s interface{ Scan(dest ...any) error }) error {
	return s.Scan(&r.id, &r.title, &r.notes, &r.dueMs, &r.completedMs,
		&r.source, &r.externalRef, &r.externalData, &r.revision, &r.createdMs, &r.updatedMs)
}

func (r *taskRow) proto(labels []string) (*taskpb.Task, error) {
	data, err := unmarshalStruct(r.externalData)
	if err != nil {
		return nil, err
	}
	return &taskpb.Task{
		Id:            r.id,
		Title:         r.title,
		Notes:         r.notes,
		Labels:        labels,
		DueTime:       tsOf(r.dueMs),
		CompletedTime: tsOf(r.completedMs),
		Source:        r.source,
		ExternalRef:   r.externalRef,
		ExternalData:  data,
		Revision:      uint64(r.revision),
		CreateTime:    msTS(r.createdMs),
		UpdateTime:    msTS(r.updatedMs),
	}, nil
}

// msOf truncates a proto timestamp to unix milliseconds; nil means NULL.
func msOf(ts *timestamppb.Timestamp) sql.NullInt64 {
	if ts == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: ts.AsTime().UnixMilli(), Valid: true}
}

func tsOf(ms sql.NullInt64) *timestamppb.Timestamp {
	if !ms.Valid {
		return nil
	}
	return msTS(ms.Int64)
}

func msTS(ms int64) *timestamppb.Timestamp {
	return timestamppb.New(time.UnixMilli(ms))
}

// unmarshalStruct decodes the stored external_data JSON; NULL means nil.
func unmarshalStruct(v sql.NullString) (*structpb.Struct, error) {
	if !v.Valid {
		return nil, nil
	}
	var s structpb.Struct
	if err := protojson.Unmarshal([]byte(v.String), &s); err != nil {
		return nil, fmt.Errorf("decode external_data: %w", err)
	}
	return &s, nil
}

// marshalStruct encodes external_data for storage. Nil and field-less structs
// both persist as NULL so the empty value has one canonical representation.
func marshalStruct(s *structpb.Struct) (sql.NullString, error) {
	if len(s.GetFields()) == 0 {
		return sql.NullString{}, nil
	}
	b, err := protojson.Marshal(s)
	if err != nil {
		return sql.NullString{}, fmt.Errorf("encode external_data: %w", err)
	}
	return sql.NullString{String: string(b), Valid: true}, nil
}

// normalizeLabels trims, rejects empties, dedupes, and sorts ascending —
// the canonical label form used on every write path.
func normalizeLabels(labels []string) ([]string, error) {
	seen := make(map[string]struct{}, len(labels))
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		l = strings.TrimSpace(l)
		if l == "" {
			return nil, fmt.Errorf("label must not be empty: %w", ErrInvalid)
		}
		if _, dup := seen[l]; dup {
			continue
		}
		seen[l] = struct{}{}
		out = append(out, l)
	}
	slices.Sort(out)
	return out, nil
}

func insertLabels(ctx context.Context, q querier, taskID string, labels []string) error {
	for _, l := range labels {
		if _, err := q.ExecContext(ctx,
			"INSERT INTO task_labels (task_id, label) VALUES (?, ?)", taskID, l); err != nil {
			return fmt.Errorf("insert label: %w", err)
		}
	}
	return nil
}

func taskLabels(ctx context.Context, q querier, taskID string) ([]string, error) {
	rows, err := q.QueryContext(ctx,
		"SELECT label FROM task_labels WHERE task_id = ? ORDER BY label", taskID)
	if err != nil {
		return nil, fmt.Errorf("load labels: %w", err)
	}
	defer rows.Close()
	var labels []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, err
		}
		labels = append(labels, l)
	}
	return labels, rows.Err()
}

func getTask(ctx context.Context, q querier, id string) (*taskpb.Task, error) {
	var r taskRow
	err := r.scan(q.QueryRowContext(ctx, "SELECT "+taskColumns+" FROM tasks WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("task %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	labels, err := taskLabels(ctx, q, id)
	if err != nil {
		return nil, err
	}
	return r.proto(labels)
}

// Create inserts a local task and returns it with server-assigned id,
// revision 1, and timestamps.
func (s *Store) Create(ctx context.Context, title, notes string, labels []string, due *timestamppb.Timestamp) (*taskpb.Task, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, fmt.Errorf("title must not be empty: %w", ErrInvalid)
	}
	labels, err := normalizeLabels(labels)
	if err != nil {
		return nil, err
	}
	id := ulid.Make().String()
	nowMs := s.now().UnixMilli()
	dueMs := msOf(due)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO tasks (id, title, notes, due_ms, revision, created_ms, updated_ms) VALUES (?, ?, ?, ?, 1, ?, ?)",
		id, title, notes, dueMs, nowMs, nowMs); err != nil {
		return nil, fmt.Errorf("insert task: %w", err)
	}
	if err := insertLabels(ctx, tx, id, labels); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &taskpb.Task{
		Id:         id,
		Title:      title,
		Notes:      notes,
		Labels:     labels,
		DueTime:    tsOf(dueMs),
		Revision:   1,
		CreateTime: msTS(nowMs),
		UpdateTime: msTS(nowMs),
	}, nil
}

// Get returns the task with the given id, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id string) (*taskpb.Task, error) {
	return getTask(ctx, s.db, id)
}

// Update applies mutate to the current state of the task in a transaction and
// persists the user-mutable fields: title, notes, labels, due_time, and
// completed_time. Mutations of id, source, external_ref, external_data,
// create_time, and revision are ignored. A nonzero expectedRevision that
// differs from the stored revision fails with ErrRevisionMismatch before
// mutate runs; errors returned by mutate propagate unwrapped.
func (s *Store) Update(ctx context.Context, id string, expectedRevision uint64, mutate func(*taskpb.Task) error) (*taskpb.Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var r taskRow
	err = r.scan(tx.QueryRowContext(ctx, "SELECT "+taskColumns+" FROM tasks WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("task %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	if expectedRevision != 0 && expectedRevision != uint64(r.revision) {
		return nil, fmt.Errorf("expected revision %d, current is %d: %w",
			expectedRevision, r.revision, ErrRevisionMismatch)
	}
	oldLabels, err := taskLabels(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	cur, err := r.proto(oldLabels)
	if err != nil {
		return nil, err
	}
	if err := mutate(cur); err != nil {
		return nil, err
	}

	title := strings.TrimSpace(cur.GetTitle())
	if title == "" {
		return nil, fmt.Errorf("title must not be empty: %w", ErrInvalid)
	}
	labels, err := normalizeLabels(cur.GetLabels())
	if err != nil {
		return nil, err
	}
	dueMs := msOf(cur.GetDueTime())
	completedMs := msOf(cur.GetCompletedTime())
	nowMs := s.now().UnixMilli()
	newRevision := r.revision + 1

	if _, err := tx.ExecContext(ctx,
		"UPDATE tasks SET title = ?, notes = ?, due_ms = ?, completed_ms = ?, revision = ?, updated_ms = ? WHERE id = ?",
		title, cur.GetNotes(), dueMs, completedMs, newRevision, nowMs, id); err != nil {
		return nil, fmt.Errorf("update task: %w", err)
	}
	// Labels are replaced wholesale.
	if _, err := tx.ExecContext(ctx, "DELETE FROM task_labels WHERE task_id = ?", id); err != nil {
		return nil, fmt.Errorf("delete labels: %w", err)
	}
	if err := insertLabels(ctx, tx, id, labels); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// Rebuild from the stored row so mutations of immutable fields vanish.
	data, err := unmarshalStruct(r.externalData)
	if err != nil {
		return nil, err
	}
	return &taskpb.Task{
		Id:            r.id,
		Title:         title,
		Notes:         cur.GetNotes(),
		Labels:        labels,
		DueTime:       tsOf(dueMs),
		CompletedTime: tsOf(completedMs),
		Source:        r.source,
		ExternalRef:   r.externalRef,
		ExternalData:  data,
		Revision:      uint64(newRevision),
		CreateTime:    msTS(r.createdMs),
		UpdateTime:    msTS(nowMs),
	}, nil
}

// Delete permanently removes a task and (via cascade) its labels.
func (s *Store) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM tasks WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("task %q: %w", id, ErrNotFound)
	}
	return nil
}
