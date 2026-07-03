package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver

	taskcorev1 "todoapp/gen/taskcore/v1"
)

const (
	defaultPageSize = 100
	maxPageSize     = 1000
)

// Open opens (creating if needed) the SQLite database at opts.Path, applies
// pending migrations, and returns the store.
//
// The DSN sets WAL journaling, busy_timeout=5000, and foreign_keys=ON as
// per-connection pragmas, and _txlock=immediate so every transaction is
// BEGIN IMMEDIATE — writers take the write lock up front instead of failing
// with SQLITE_BUSY at upgrade time.
func Open(opts Options) (Store, error) {
	dsn := "file:" + opts.Path + "?" + strings.Join([]string{
		"_txlock=immediate",
		"_pragma=journal_mode(WAL)",
		"_pragma=busy_timeout(5000)",
		"_pragma=foreign_keys(1)",
	}, "&")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", opts.Path, err)
	}
	if err := applyMigrations(db); err != nil {
		db.Close()
		return nil, err
	}
	return &sqliteStore{db: db, opts: opts}, nil
}

type sqliteStore struct {
	db   *sql.DB
	opts Options
}

func (s *sqliteStore) Close() error { return s.db.Close() }

// --- items ---

func (s *sqliteStore) CreateItem(ctx context.Context, item *taskcorev1.Item, prov *taskcorev1.Provenance) (*taskcorev1.Event, error) {
	now := s.opts.Now()
	stored := proto.Clone(item).(*taskcorev1.Item)
	stored.UpdatedAt = timestamppb.New(now) // created_at is the caller's; updated_at is ours

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin create: %w", err)
	}
	defer tx.Rollback()

	blob, args, err := itemArgs(stored, s.opts.Extract)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO items (id, kind, blob, completed, project, due_unix, snoozed_until_unix, created_at_unix, updated_at_unix, connector_instance, external_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		append([]any{stored.GetId(), stored.GetKind(), blob}, args...)...,
	); err != nil {
		return nil, fmt.Errorf("store: insert item %s: %w", stored.GetId(), err)
	}

	evt := &taskcorev1.Event{
		ItemId:   stored.GetId(),
		Type:     taskcorev1.ChangeType_CHANGE_TYPE_CREATED,
		CausedBy: prov,
		Item:     stored, // after-image; changes stay empty for CREATED
	}
	if err := appendEvent(ctx, tx, now, evt); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit create: %w", err)
	}
	return evt, nil
}

func (s *sqliteStore) MutateItem(ctx context.Context, id string, prov *taskcorev1.Provenance, fn func(item *taskcorev1.Item) error) (*taskcorev1.Item, *taskcorev1.Event, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("store: begin mutate: %w", err)
	}
	defer tx.Rollback()

	before, err := getItem(ctx, tx, id)
	if err != nil {
		return nil, nil, err
	}
	after := proto.Clone(before).(*taskcorev1.Item)
	if err := fn(after); err != nil {
		return nil, nil, err
	}
	changes, err := s.opts.Diff(before, after)
	if err != nil {
		return nil, nil, fmt.Errorf("store: diff item %s: %w", id, err)
	}
	if len(changes) == 0 {
		// No-op mutation: write nothing, emit nothing — this is what
		// silences sync echoes by value.
		return before, nil, nil
	}

	todoTouched, mirrorTouched := false, false
	for _, c := range changes {
		p := c.GetPath()
		// Relations bump NEITHER revision (phase-2 decision): they are
		// core-owned wiring written by both sync (series INSTANCE_OF) and,
		// later, users/rules — tying them to either layer's counter would
		// cause exactly the false conflicts the split exists to prevent.
		// They are versioned by updated_at; a dedicated concurrency token
		// can come with the relations-editing API.
		if strings.HasPrefix(p, "todo.") {
			todoTouched = true
		}
		if strings.HasPrefix(p, "mirror.") {
			mirrorTouched = true
		}
	}
	if todoTouched {
		after.TodoRevision++
	}
	if mirrorTouched {
		after.MirrorRevision++
	}
	now := s.opts.Now()
	after.UpdatedAt = timestamppb.New(now)

	blob, args, err := itemArgs(after, s.opts.Extract)
	if err != nil {
		return nil, nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE items SET kind = ?, blob = ?, completed = ?, project = ?, due_unix = ?, snoozed_until_unix = ?, created_at_unix = ?, updated_at_unix = ?, connector_instance = ?, external_id = ?
		 WHERE id = ?`,
		append(append([]any{after.GetKind(), blob}, args...), id)...,
	); err != nil {
		return nil, nil, fmt.Errorf("store: update item %s: %w", id, err)
	}

	evt := &taskcorev1.Event{
		ItemId:   id,
		Type:     taskcorev1.ChangeType_CHANGE_TYPE_UPDATED,
		CausedBy: prov,
		Changes:  changes,
		Item:     after,
	}
	if err := appendEvent(ctx, tx, now, evt); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("store: commit mutate: %w", err)
	}
	return after, evt, nil
}

func (s *sqliteStore) DeleteItem(ctx context.Context, id string, prov *taskcorev1.Provenance) (*taskcorev1.Event, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin delete: %w", err)
	}
	defer tx.Rollback()

	last, err := getItem(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM items WHERE id = ?`, id); err != nil {
		return nil, fmt.Errorf("store: delete item %s: %w", id, err)
	}
	evt := &taskcorev1.Event{
		ItemId:   id,
		Type:     taskcorev1.ChangeType_CHANGE_TYPE_DELETED,
		CausedBy: prov,
		Item:     last, // last state rides as the after-image
	}
	if err := appendEvent(ctx, tx, s.opts.Now(), evt); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit delete: %w", err)
	}
	return evt, nil
}

func (s *sqliteStore) GetItem(ctx context.Context, id string) (*taskcorev1.Item, error) {
	return getItem(ctx, s.db, id)
}

func (s *sqliteStore) ResolveIDPrefix(ctx context.Context, prefix string) (string, error) {
	// An exact id wins even when it prefixes other ids.
	var exact string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM items WHERE id = ?`, prefix).Scan(&exact)
	if err == nil {
		return exact, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("store: resolve prefix %q: %w", prefix, err)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM items WHERE id LIKE ? LIMIT 2`, prefix+"%")
	if err != nil {
		return "", fmt.Errorf("store: resolve prefix %q: %w", prefix, err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	switch len(ids) {
	case 0:
		return "", ErrNotFound
	case 1:
		return ids[0], nil
	default:
		return "", ErrAmbiguousPrefix
	}
}

// orderClauses is the OrderBy allowlist. For "due", the leading
// "due_unix IS NULL" keeps NULL dues last in both directions.
var orderClauses = map[string]struct{ asc, desc string }{
	"due": {
		asc:  "due_unix IS NULL, due_unix ASC, id ASC",
		desc: "due_unix IS NULL, due_unix DESC, id DESC",
	},
	"created_at": {asc: "created_at_unix ASC, id ASC", desc: "created_at_unix DESC, id DESC"},
	"updated_at": {asc: "updated_at_unix ASC, id ASC", desc: "updated_at_unix DESC, id DESC"},
	"project":    {asc: "project ASC, id ASC", desc: "project DESC, id DESC"},
	"kind":       {asc: "kind ASC, id ASC", desc: "kind DESC, id DESC"},
}

func (s *sqliteStore) QueryItems(ctx context.Context, q Query) (*QueryResult, error) {
	orderBy, desc := q.OrderBy, q.Desc
	if orderBy == "" {
		// Server default: newest first.
		orderBy, desc = "created_at", true
	}
	clauses, ok := orderClauses[orderBy]
	if !ok {
		return nil, fmt.Errorf("store: invalid order_by %q", q.OrderBy)
	}
	orderClause := clauses.asc
	if desc {
		orderClause = clauses.desc
	}

	pageSize := q.PageSize
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}

	offset, err := decodePageToken(q.PageToken)
	if err != nil {
		return nil, err
	}

	// One connection for the whole call so the returned Cursor and the row
	// scan see the same database. The cursor is read first: if a write lands
	// between the two reads the cursor only under-reports, and replaying
	// already-applied events after a snapshot is safe (the reverse is not).
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: query: %w", err)
	}
	defer conn.Close()

	var cursor uint64
	if err := conn.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(cursor), 0) FROM events`).Scan(&cursor); err != nil {
		return nil, fmt.Errorf("store: query cursor: %w", err)
	}

	query := `SELECT blob FROM items`
	args := append([]any{}, q.Args...)
	if q.Where != "" {
		query += " WHERE " + q.Where
	}
	query += " ORDER BY " + orderClause + " LIMIT -1 OFFSET ?"
	args = append(args, offset)

	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: query items: %w", err)
	}
	defer rows.Close()

	var items []*taskcorev1.Item
	consumed := 0
	more := false
	for rows.Next() {
		if len(items) == pageSize {
			// A row exists beyond the full page; it was not consumed, so the
			// next page's offset points at it.
			more = true
			break
		}
		var blob []byte
		if err := rows.Scan(&blob); err != nil {
			return nil, err
		}
		consumed++
		item := &taskcorev1.Item{}
		if err := proto.Unmarshal(blob, item); err != nil {
			return nil, fmt.Errorf("store: unmarshal item: %w", err)
		}
		if q.Residual != nil {
			keep, err := q.Residual(item)
			if err != nil {
				return nil, err
			}
			if !keep {
				continue
			}
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	res := &QueryResult{Items: items, Cursor: cursor}
	if more {
		res.NextPageToken = encodePageToken(offset + consumed)
	}
	return res, nil
}

// Page tokens are base64 of the count of SQL rows consumed so far — an
// opaque offset into the SQL row stream, deterministic given the stable
// (column, id) ordering. This is an internal token format and may be
// replaced; clients must treat tokens as opaque.
func encodePageToken(offset int) string {
	return base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func decodePageToken(token string) (int, error) {
	if token == "" {
		return 0, nil
	}
	raw, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return 0, ErrInvalidPageToken
	}
	n, err := strconv.Atoi(string(raw))
	if err != nil || n < 0 {
		return 0, ErrInvalidPageToken
	}
	return n, nil
}

// --- events ---

func (s *sqliteStore) ListEvents(ctx context.Context, sinceCursor uint64, limit int) ([]*taskcorev1.Event, error) {
	if limit <= 0 {
		limit = -1 // no limit
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT cursor, payload FROM events WHERE cursor > ? ORDER BY cursor ASC LIMIT ?`,
		sinceCursor, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list events: %w", err)
	}
	defer rows.Close()
	var events []*taskcorev1.Event
	for rows.Next() {
		var cursor uint64
		var payload []byte
		if err := rows.Scan(&cursor, &payload); err != nil {
			return nil, err
		}
		evt := &taskcorev1.Event{}
		if err := proto.Unmarshal(payload, evt); err != nil {
			return nil, fmt.Errorf("store: unmarshal event %d: %w", cursor, err)
		}
		evt.Cursor = cursor // the column is authoritative; the payload stores 0
		events = append(events, evt)
	}
	return events, rows.Err()
}

func (s *sqliteStore) LatestCursor(ctx context.Context) (uint64, error) {
	var c uint64
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(cursor), 0) FROM events`).Scan(&c)
	return c, err
}

func (s *sqliteStore) OldestCursor(ctx context.Context) (uint64, error) {
	var c uint64
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MIN(cursor), 0) FROM events`).Scan(&c)
	return c, err
}

func (s *sqliteStore) TrimEvents(ctx context.Context, cutoff time.Time) (int64, error) {
	// The MAX(cursor) row always survives so LatestCursor outlives a full trim.
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM events
		 WHERE recorded_at_unix < ? AND cursor <> (SELECT MAX(cursor) FROM events)`,
		cutoff.UnixNano())
	if err != nil {
		return 0, fmt.Errorf("store: trim events: %w", err)
	}
	return res.RowsAffected()
}

// --- views ---

func (s *sqliteStore) SaveView(ctx context.Context, view *taskcorev1.View) error {
	payload, err := proto.Marshal(view)
	if err != nil {
		return fmt.Errorf("store: marshal view %s: %w", view.GetName(), err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO views (name, payload) VALUES (?, ?)
		 ON CONFLICT(name) DO UPDATE SET payload = excluded.payload`,
		view.GetName(), payload); err != nil {
		return fmt.Errorf("store: save view %s: %w", view.GetName(), err)
	}
	return nil
}

func (s *sqliteStore) GetView(ctx context.Context, name string) (*taskcorev1.View, error) {
	var payload []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT payload FROM views WHERE name = ?`, name).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get view %s: %w", name, err)
	}
	view := &taskcorev1.View{}
	if err := proto.Unmarshal(payload, view); err != nil {
		return nil, fmt.Errorf("store: unmarshal view %s: %w", name, err)
	}
	return view, nil
}

func (s *sqliteStore) ListViews(ctx context.Context) ([]*taskcorev1.View, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM views ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("store: list views: %w", err)
	}
	defer rows.Close()
	var views []*taskcorev1.View
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		view := &taskcorev1.View{}
		if err := proto.Unmarshal(payload, view); err != nil {
			return nil, fmt.Errorf("store: unmarshal view: %w", err)
		}
		views = append(views, view)
	}
	return views, rows.Err()
}

// DeleteView is idempotent: deleting a missing view is not an error.
func (s *sqliteStore) DeleteView(ctx context.Context, name string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM views WHERE name = ?`, name); err != nil {
		return fmt.Errorf("store: delete view %s: %w", name, err)
	}
	return nil
}

// --- backup ---

func (s *sqliteStore) Backup(ctx context.Context, destPath string) (int64, error) {
	// VACUUM INTO produces a consistent, compacted copy and is safe against
	// a live WAL database. It refuses to overwrite an existing file; that
	// sqlite error is returned wrapped — callers pick fresh paths.
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, destPath); err != nil {
		return 0, fmt.Errorf("store: backup to %s: %w", destPath, err)
	}
	fi, err := os.Stat(destPath)
	if err != nil {
		return 0, fmt.Errorf("store: stat backup %s: %w", destPath, err)
	}
	return fi.Size(), nil
}

// --- helpers ---

// rowQuerier is satisfied by *sql.DB, *sql.Tx, and *sql.Conn.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func getItem(ctx context.Context, q rowQuerier, id string) (*taskcorev1.Item, error) {
	var blob []byte
	err := q.QueryRowContext(ctx, `SELECT blob FROM items WHERE id = ?`, id).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get item %s: %w", id, err)
	}
	item := &taskcorev1.Item{}
	if err := proto.Unmarshal(blob, item); err != nil {
		return nil, fmt.Errorf("store: unmarshal item %s: %w", id, err)
	}
	return item, nil
}

// itemArgs marshals the item and derives the indexed column values
// (completed, project, due_unix, snoozed_until_unix, created_at_unix,
// updated_at_unix) via opts.Extract.
func itemArgs(item *taskcorev1.Item, extract func(*taskcorev1.Item) Indexed) ([]byte, []any, error) {
	blob, err := proto.Marshal(item)
	if err != nil {
		return nil, nil, fmt.Errorf("store: marshal item %s: %w", item.GetId(), err)
	}
	idx := extract(item)
	completed := 0
	if idx.Completed {
		completed = 1
	}
	var due, snoozed any
	if idx.Due != nil {
		due = idx.Due.UnixNano()
	}
	if idx.SnoozedUntil != nil {
		snoozed = idx.SnoozedUntil.UnixNano()
	}
	args := []any{
		completed,
		idx.Project,
		due,
		snoozed,
		item.GetCreatedAt().AsTime().UnixNano(),
		item.GetUpdatedAt().AsTime().UnixNano(),
		item.GetMirror().GetLink().GetConnectorInstance(),
		item.GetMirror().GetLink().GetExternalId(),
	}
	return blob, args, nil
}

// appendEvent inserts the event into the log inside the caller's
// transaction. The payload is marshaled with cursor 0 — the AUTOINCREMENT
// column is the authoritative cursor and is set on evt after insert.
func appendEvent(ctx context.Context, tx *sql.Tx, now time.Time, evt *taskcorev1.Event) error {
	evt.Cursor = 0
	payload, err := proto.Marshal(evt)
	if err != nil {
		return fmt.Errorf("store: marshal event: %w", err)
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO events (item_id, recorded_at_unix, payload) VALUES (?, ?, ?)`,
		evt.GetItemId(), now.UnixNano(), payload)
	if err != nil {
		return fmt.Errorf("store: insert event: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: event cursor: %w", err)
	}
	evt.Cursor = uint64(id)
	return nil
}
