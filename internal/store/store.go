// Package store is the persistence boundary: items, the append-only event
// log, saved views, and backup. Item mutations and their events commit in
// the same transaction — the log can never disagree with state.
package store

import (
	"context"
	"errors"
	"time"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
	taskcorev1 "todoapp/gen/taskcore/v1"
)

var (
	// ErrNotFound is returned when an item, view, or id prefix matches nothing.
	ErrNotFound = errors.New("store: not found")
	// ErrAmbiguousPrefix is returned when an id prefix matches multiple items.
	ErrAmbiguousPrefix = errors.New("store: ambiguous id prefix")
	// ErrInvalidPageToken is returned for malformed or stale page tokens.
	ErrInvalidPageToken = errors.New("store: invalid page token")
)

// Store is implemented by the SQLite backend. The interface is the seam for
// tests and any future backend; it is deliberately free of SQL concepts
// except Query.Where, which speaks the indexed-column vocabulary below.
type Store interface {
	// CreateItem persists a new item and appends a CREATED event atomically.
	// The item must arrive fully formed (id, kind, revisions, timestamps set
	// by the caller).
	CreateItem(ctx context.Context, item *taskcorev1.Item, prov *taskcorev1.Provenance) (*taskcorev1.Event, error)

	// MutateItem loads the item, applies fn to a deep copy, diffs
	// before/after, and persists item + UPDATED event atomically. A no-op
	// mutation (empty diff) writes nothing and returns (item, nil, nil).
	// fn returning an error aborts with that error. The store bumps
	// todo_revision / mirror_revision according to which layers the diff
	// touched, and sets updated_at; fn must not touch those fields.
	MutateItem(ctx context.Context, id string, prov *taskcorev1.Provenance, fn func(item *taskcorev1.Item) error) (*taskcorev1.Item, *taskcorev1.Event, error)

	// DeleteItem removes the item and appends a DELETED event (carrying the
	// last state) atomically. Kind policy (native-only) is enforced by the
	// server layer, not here.
	DeleteItem(ctx context.Context, id string, prov *taskcorev1.Provenance) (*taskcorev1.Event, error)

	GetItem(ctx context.Context, id string) (*taskcorev1.Item, error)

	// ResolveIDPrefix expands a unique id prefix to a full id
	// (ErrNotFound / ErrAmbiguousPrefix otherwise). Exact ids resolve to
	// themselves even if they prefix other ids.
	ResolveIDPrefix(ctx context.Context, prefix string) (string, error)

	QueryItems(ctx context.Context, q Query) (*QueryResult, error)

	// ListEvents returns up to limit events with cursor > sinceCursor, in
	// cursor order.
	ListEvents(ctx context.Context, sinceCursor uint64, limit int) ([]*taskcorev1.Event, error)
	// LatestCursor returns the newest assigned cursor (0 when the log is empty).
	LatestCursor(ctx context.Context) (uint64, error)
	// OldestCursor returns the oldest retained cursor (0 when the log is
	// empty). A Watch from below this must CURSOR_EXPIRED.
	OldestCursor(ctx context.Context) (uint64, error)
	// TrimEvents deletes events recorded before cutoff, always retaining the
	// most recent event so LatestCursor survives trimming. Returns the count
	// removed.
	TrimEvents(ctx context.Context, cutoff time.Time) (int64, error)

	// GetItemByExternal looks up the item mirroring (instance, externalID);
	// ErrNotFound when nothing mirrors it.
	GetItemByExternal(ctx context.Context, instance, externalID string) (*taskcorev1.Item, error)
	// ListInstanceItems returns every item whose mirror link belongs to the
	// connector instance (the sync engine's reconciliation set — bounded by
	// the instance's scope, e.g. a calendar horizon).
	ListInstanceItems(ctx context.Context, instance string) ([]*taskcorev1.Item, error)

	// Manifests are persisted so kinds stay renderable, filterable, and
	// exportable after their plugin is uninstalled.
	SaveManifest(ctx context.Context, m *pluginv1.Manifest) error
	GetManifest(ctx context.Context, plugin string) (*pluginv1.Manifest, error)
	ListManifests(ctx context.Context) ([]*pluginv1.Manifest, error)

	// Rules: the store is the source of truth (files are import/export).
	// ListRules returns evaluation order (position, then name); SaveRule
	// with position 0 appends.
	SaveRule(ctx context.Context, rule *taskcorev1.Rule) error
	GetRule(ctx context.Context, name string) (*taskcorev1.Rule, error)
	ListRules(ctx context.Context) ([]*taskcorev1.Rule, error)
	DeleteRule(ctx context.Context, name string) error

	// Meta is a small KV for engine bookkeeping (e.g. the rules engine's
	// durable event cursor). GetMeta returns "" for absent keys.
	GetMeta(ctx context.Context, key string) (string, error)
	SetMeta(ctx context.Context, key, value string) error

	// The outbox (phase 4): durable intent records. EnqueueIntent inserts
	// (id must be unique); UpdateIntent replaces the full record by id
	// (ErrNotFound when absent). ListIntents filters by states (empty means
	// QUEUED+INFLIGHT+FAILED) and optional item id, newest first, capped by
	// limit (<=0 means 200). DueIntents returns QUEUED records with
	// next_attempt_at <= now (or unset), oldest first — the worker's feed.
	// RequeueStaleInflight flips INFLIGHT records older than cutoff back to
	// QUEUED (crash recovery) and reports how many.
	EnqueueIntent(ctx context.Context, rec *taskcorev1.IntentRecord) error
	GetIntent(ctx context.Context, id string) (*taskcorev1.IntentRecord, error)
	UpdateIntent(ctx context.Context, rec *taskcorev1.IntentRecord) error
	ListIntents(ctx context.Context, states []taskcorev1.IntentState, itemID string, limit int) ([]*taskcorev1.IntentRecord, error)
	DueIntents(ctx context.Context, now time.Time, limit int) ([]*taskcorev1.IntentRecord, error)
	RequeueStaleInflight(ctx context.Context, cutoff time.Time) (int, error)

	SaveView(ctx context.Context, view *taskcorev1.View) error
	GetView(ctx context.Context, name string) (*taskcorev1.View, error)
	ListViews(ctx context.Context) ([]*taskcorev1.View, error)
	DeleteView(ctx context.Context, name string) error

	// Backup writes a consistent online copy of the database to destPath
	// (safe against a live WAL database) and returns its size in bytes.
	Backup(ctx context.Context, destPath string) (int64, error)

	Close() error
}

// Options configures the SQLite store. Diff and Extract are injected so the
// store depends on behavior, not on the feed/query packages.
type Options struct {
	// Path of the database file.
	Path string
	// Diff computes FieldChanges between before and after (see feed.Diff).
	Diff func(before, after *taskcorev1.Item) ([]*taskcorev1.FieldChange, error)
	// Extract derives the indexed columns from an item (see query.Extract).
	Extract func(item *taskcorev1.Item) Indexed
	// Now is the injected clock (used for event timestamps and trims).
	Now func() time.Time
}

// Indexed holds the extracted, indexed columns kept alongside the item blob.
// These are exactly the fields Query.Where and OrderBy may reference.
type Indexed struct {
	Completed    bool
	Project      string
	Due          *time.Time // effective due (override-wins)
	SnoozedUntil *time.Time
}

// Query is a compiled item query. Where/Args speak SQL over the indexed
// columns (kind, completed, project, due_unix, snoozed_until_unix,
// created_at_unix, updated_at_unix); Residual is evaluated per row after the
// SQL narrowing. Either may be zero-valued.
type Query struct {
	Where    string
	Args     []any
	Residual func(item *taskcorev1.Item) (bool, error)
	// OrderBy is one of: due, created_at, updated_at, project, kind.
	OrderBy string
	Desc    bool
	// PageSize <= 0 means the server default.
	PageSize  int
	PageToken string
}

// QueryResult is one page of results plus the feed cursor the snapshot is
// consistent with (for gapless snapshot-then-watch).
type QueryResult struct {
	Items         []*taskcorev1.Item
	NextPageToken string
	Cursor        uint64
}
