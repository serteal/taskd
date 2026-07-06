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

	taskpb "github.com/serteal/taskd/gen/task"
	"github.com/serteal/taskd/internal/recur"
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
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
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

const taskColumns = "id, title, notes, due_ms, completed_ms, source, external_ref, external_data, user_data, recurrence, parent_id, revision, created_ms, updated_ms"

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
	userData     sql.NullString
	recurrence   string
	parentID     string
	revision     int64
	createdMs    int64
	updatedMs    int64
}

func (r *taskRow) scan(s interface{ Scan(dest ...any) error }) error {
	return s.Scan(&r.id, &r.title, &r.notes, &r.dueMs, &r.completedMs,
		&r.source, &r.externalRef, &r.externalData, &r.userData,
		&r.recurrence, &r.parentID, &r.revision, &r.createdMs, &r.updatedMs)
}

func (r *taskRow) proto(labels []string) (*taskpb.Task, error) {
	data, err := unmarshalStruct(r.externalData)
	if err != nil {
		return nil, err
	}
	userData, err := unmarshalStruct(r.userData)
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
		UserData:      userData,
		Recurrence:    r.recurrence,
		ParentId:      r.parentID,
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
// revision 1, and timestamps. recurrence (a canonical RRULE subset, or "")
// and parentID (a top-level task's id, or "") are validated before insert.
func (s *Store) Create(ctx context.Context, title, notes string, labels []string, due *timestamppb.Timestamp, recurrence, parentID string) (*taskpb.Task, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, fmt.Errorf("title must not be empty: %w", ErrInvalid)
	}
	labels, err := normalizeLabels(labels)
	if err != nil {
		return nil, err
	}
	// A local task's source is always "", so recurrence is always permitted.
	if err := validateRecurrence(recurrence, ""); err != nil {
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
	if err := validateParent(ctx, tx, id, parentID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO tasks (id, title, notes, due_ms, recurrence, parent_id, revision, created_ms, updated_ms) VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?)",
		id, title, notes, dueMs, recurrence, parentID, nowMs, nowMs); err != nil {
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
		Recurrence: recurrence,
		ParentId:   parentID,
		Revision:   1,
		CreateTime: msTS(nowMs),
		UpdateTime: msTS(nowMs),
	}, nil
}

// validateRecurrence rejects a recurrence that is malformed or set on a synced
// task (source != ""). An empty recurrence is always valid.
func validateRecurrence(recurrence, source string) error {
	if recurrence == "" {
		return nil
	}
	if source != "" {
		return fmt.Errorf("recurrence cannot be set on synced task (source %q): %w", source, ErrInvalid)
	}
	if _, err := recur.Parse(recurrence); err != nil {
		return fmt.Errorf("%v: %w", err, ErrInvalid)
	}
	return nil
}

// validateParent enforces the one-level hierarchy for parenting taskID under
// parentID within q: the parent must exist (ErrNotFound) and be top-level, the
// task must not be its own parent, and the task must not already have children
// (both depth violations are ErrInvalid). An empty parentID is always valid.
func validateParent(ctx context.Context, q querier, taskID, parentID string) error {
	if parentID == "" {
		return nil
	}
	if parentID == taskID {
		return fmt.Errorf("a task cannot be its own parent: %w", ErrInvalid)
	}
	var parentParent string
	err := q.QueryRowContext(ctx, "SELECT parent_id FROM tasks WHERE id = ?", parentID).Scan(&parentParent)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("parent task %q: %w", parentID, ErrNotFound)
	}
	if err != nil {
		return err
	}
	if parentParent != "" {
		return fmt.Errorf("parent %q is itself a subtask; subtasks are one level deep: %w", parentID, ErrInvalid)
	}
	var one int
	err = q.QueryRowContext(ctx, "SELECT 1 FROM tasks WHERE parent_id = ? LIMIT 1", taskID).Scan(&one)
	if err == nil {
		return fmt.Errorf("task %q has subtasks and cannot itself become a subtask; subtasks are one level deep: %w", taskID, ErrInvalid)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}

// Get returns the task with the given id, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id string) (*taskpb.Task, error) {
	return getTask(ctx, s.db, id)
}

// Update applies mutate to the current state of the task in a transaction and
// persists the user-mutable fields: title, notes, labels, due_time,
// completed_time, user_data, recurrence, and parent_id. Mutations of id,
// source, external_ref, external_data, create_time, and revision are ignored.
// A nonzero expectedRevision that differs from the stored revision fails with
// ErrRevisionMismatch before mutate runs; errors returned by mutate propagate
// unwrapped.
//
// Recurrence roll-forward: if mutate completes an active (incomplete)
// recurring task — the stored task had no completed_time and mutate sets a
// non-zero one while recurrence != "" — the task is NOT completed. Instead this
// inserts a frozen archive copy (a new local task with the same fields,
// completed at the requested time, no recurrence) and advances the live task's
// due_time to the next occurrence, leaving it active. The advanced live task is
// returned as the first result and the archive as the second (spawned); on
// every other update — including adding recurrence to an already-completed task
// or re-masking completed on one — the second result is nil.
func (s *Store) Update(ctx context.Context, id string, expectedRevision uint64, mutate func(*taskpb.Task) error) (*taskpb.Task, *taskpb.Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	var r taskRow
	err = r.scan(tx.QueryRowContext(ctx, "SELECT "+taskColumns+" FROM tasks WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, fmt.Errorf("task %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, nil, err
	}
	if expectedRevision != 0 && expectedRevision != uint64(r.revision) {
		return nil, nil, fmt.Errorf("expected revision %d, current is %d: %w",
			expectedRevision, r.revision, ErrRevisionMismatch)
	}
	oldLabels, err := taskLabels(ctx, tx, id)
	if err != nil {
		return nil, nil, err
	}
	cur, err := r.proto(oldLabels)
	if err != nil {
		return nil, nil, err
	}
	if err := mutate(cur); err != nil {
		return nil, nil, err
	}

	title := strings.TrimSpace(cur.GetTitle())
	if title == "" {
		return nil, nil, fmt.Errorf("title must not be empty: %w", ErrInvalid)
	}
	labels, err := normalizeLabels(cur.GetLabels())
	if err != nil {
		return nil, nil, err
	}
	if err := validateRecurrence(cur.GetRecurrence(), r.source); err != nil {
		return nil, nil, err
	}
	if cur.GetParentId() != r.parentID {
		if err := validateParent(ctx, tx, id, cur.GetParentId()); err != nil {
			return nil, nil, err
		}
	}
	userData, err := marshalStruct(cur.GetUserData())
	if err != nil {
		return nil, nil, err
	}
	nowT := s.now()
	nowMs := nowT.UnixMilli()
	newRevision := r.revision + 1

	// Roll the series forward only on the incomplete->completed transition of an
	// active (incomplete) recurring task: the stored pre-mutate row had no
	// completed_ms (r.completedMs is NULL) and the mask just set one. Adding
	// recurrence to an already-completed task, or re-masking completed on one,
	// is a plain update that leaves the task completed — never a resurrection.
	var spawnedID string
	justCompleted := !r.completedMs.Valid && cur.GetCompletedTime() != nil
	if cur.GetRecurrence() != "" && justCompleted {
		spawnedID, err = s.rollForward(ctx, tx, cur, labels, r.dueMs, nowT)
		if err != nil {
			return nil, nil, err
		}
	} else {
		if _, err := tx.ExecContext(ctx,
			"UPDATE tasks SET title = ?, notes = ?, due_ms = ?, completed_ms = ?, user_data = ?, recurrence = ?, parent_id = ?, revision = ?, updated_ms = ? WHERE id = ?",
			title, cur.GetNotes(), msOf(cur.GetDueTime()), msOf(cur.GetCompletedTime()), userData,
			cur.GetRecurrence(), cur.GetParentId(), newRevision, nowMs, id); err != nil {
			return nil, nil, fmt.Errorf("update task: %w", err)
		}
		if err := replaceLabels(ctx, tx, id, labels); err != nil {
			return nil, nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}

	updated, err := getTask(ctx, s.db, id)
	if err != nil {
		return nil, nil, err
	}
	if spawnedID == "" {
		return updated, nil, nil
	}
	spawned, err := getTask(ctx, s.db, spawnedID)
	if err != nil {
		return nil, nil, err
	}
	return updated, spawned, nil
}

// rollForward performs one recurrence step inside tx: it inserts a frozen
// archive of the occurrence just finished (completed at cur's completed_time,
// no recurrence) and advances the live task's due_time to the next occurrence,
// leaving it active. archiveDue is the task's PRE-mutate due — the occurrence
// actually completed — which the archive records even when this same update
// also changed due_time; the advance base is the post-mutate due, since an
// explicit due change expresses where the series continues from. Recurrence
// math runs in nowT's location so occurrences keep their local wall-clock time
// across DST shifts (the daemon's timezone is authoritative). It returns the
// archive's id.
func (s *Store) rollForward(ctx context.Context, tx *sql.Tx, cur *taskpb.Task, labels []string, archiveDue sql.NullInt64, nowT time.Time) (string, error) {
	rule, err := recur.Parse(cur.GetRecurrence())
	if err != nil {
		return "", fmt.Errorf("%v: %w", err, ErrInvalid)
	}
	// Next occurrence: strictly after the old due, then fast-forward past now so
	// an overdue task completes once, not once per missed occurrence. With no
	// due date the series anchors on now. The advance base is taken in the
	// daemon's local zone so calendar math preserves the wall-clock time of day
	// (e.g. 09:00) even when the interval crosses a DST boundary.
	base := nowT
	if cur.GetDueTime() != nil {
		base = cur.GetDueTime().AsTime().In(nowT.Location())
	}
	next := rule.Next(base)
	for !next.After(nowT) {
		next = rule.Next(next)
	}

	userData, err := marshalStruct(cur.GetUserData())
	if err != nil {
		return "", err
	}
	nowMs := nowT.UnixMilli()

	// (1) Frozen archive copy: new id, revision 1, no recurrence, completed. Its
	// due is the PRE-mutate due (the occurrence that was finished), not any new
	// due_time this same update set.
	archiveID := ulid.Make().String()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO tasks (id, title, notes, due_ms, completed_ms, user_data, parent_id, revision, created_ms, updated_ms)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
		archiveID, strings.TrimSpace(cur.GetTitle()), cur.GetNotes(), archiveDue,
		msOf(cur.GetCompletedTime()), userData, cur.GetParentId(), nowMs, nowMs); err != nil {
		return "", fmt.Errorf("insert occurrence archive: %w", err)
	}
	if err := insertLabels(ctx, tx, archiveID, labels); err != nil {
		return "", err
	}

	// (2) Advance the live task; it stays active with the next due date.
	if _, err := tx.ExecContext(ctx,
		"UPDATE tasks SET title = ?, notes = ?, due_ms = ?, completed_ms = NULL, user_data = ?, recurrence = ?, parent_id = ?, revision = revision + 1, updated_ms = ? WHERE id = ?",
		strings.TrimSpace(cur.GetTitle()), cur.GetNotes(), msOf(timestampOf(next)), userData,
		cur.GetRecurrence(), cur.GetParentId(), nowMs, cur.GetId()); err != nil {
		return "", fmt.Errorf("advance recurring task: %w", err)
	}
	if err := replaceLabels(ctx, tx, cur.GetId(), labels); err != nil {
		return "", err
	}
	return archiveID, nil
}

// timestampOf wraps a time in a proto timestamp so msOf can truncate it.
func timestampOf(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t) }

// replaceLabels swaps a task's label set for the given labels.
func replaceLabels(ctx context.Context, q querier, taskID string, labels []string) error {
	if _, err := q.ExecContext(ctx, "DELETE FROM task_labels WHERE task_id = ?", taskID); err != nil {
		return fmt.Errorf("delete labels: %w", err)
	}
	return insertLabels(ctx, q, taskID, labels)
}

// Delete permanently removes a task and (via cascade) its labels. Its children
// are re-parented to top-level in the same transaction rather than deleted;
// their new states are returned so the caller can fan out watch events.
func (s *Store) Delete(ctx context.Context, id string) ([]*taskpb.Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	childIDs, err := childIDsOf(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(ctx, "DELETE FROM tasks WHERE id = ?", id)
	if err != nil {
		return nil, fmt.Errorf("delete task: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, fmt.Errorf("task %q: %w", id, ErrNotFound)
	}
	reparented, err := reparentToRoot(ctx, tx, childIDs, s.now().UnixMilli())
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return reparented, nil
}

// childIDsOf returns the ids of tasks whose parent is parentID, drained fully
// (the single-connection pool forbids leaving a result set open).
func childIDsOf(ctx context.Context, q querier, parentID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, "SELECT id FROM tasks WHERE parent_id = ? ORDER BY id", parentID)
	if err != nil {
		return nil, fmt.Errorf("find children: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var cid string
		if err := rows.Scan(&cid); err != nil {
			return nil, err
		}
		ids = append(ids, cid)
	}
	return ids, rows.Err()
}

// reparentToRoot clears parent_id on the given tasks (bumping revision) and
// returns their new states, for watch fan-out.
func reparentToRoot(ctx context.Context, q querier, ids []string, nowMs int64) ([]*taskpb.Task, error) {
	for _, cid := range ids {
		if _, err := q.ExecContext(ctx,
			"UPDATE tasks SET parent_id = '', revision = revision + 1, updated_ms = ? WHERE id = ?",
			nowMs, cid); err != nil {
			return nil, fmt.Errorf("re-parent child: %w", err)
		}
	}
	out := make([]*taskpb.Task, 0, len(ids))
	for _, cid := range ids {
		t, err := getTask(ctx, q, cid)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}
