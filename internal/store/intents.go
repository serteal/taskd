package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

// The outbox (phase 4): durable intent records. payload is the full marshaled
// IntentRecord; the indexed columns (state, item_id, next_attempt_at_unix,
// created_at_unix) are extracted from it on every write and kept in sync so
// ListIntents/DueIntents filter and order without unmarshaling. Every write
// stamps updated_at from opts.Now, exactly as item writes do; all other
// timestamps are the caller's.

const (
	defaultIntentLimit = 200
	maxIntentLimit     = 1000
	defaultDueLimit    = 100
)

// defaultIntentStates is the ListIntents filter when the caller passes none:
// the live outbox (queued, in flight, or failed and awaiting a decision),
// excluding the terminal CONFIRMED/DISCARDED.
var defaultIntentStates = []taskcorev1.IntentState{
	taskcorev1.IntentState_INTENT_STATE_QUEUED,
	taskcorev1.IntentState_INTENT_STATE_INFLIGHT,
	taskcorev1.IntentState_INTENT_STATE_FAILED,
}

func (s *sqliteStore) EnqueueIntent(ctx context.Context, rec *taskcorev1.IntentRecord) error {
	if rec.GetId() == "" {
		return fmt.Errorf("store: intent id is required")
	}
	if rec.GetItemId() == "" {
		return fmt.Errorf("store: intent item_id is required")
	}
	stored := proto.Clone(rec).(*taskcorev1.IntentRecord)
	stored.UpdatedAt = timestamppb.New(s.opts.Now()) // updated_at is ours; the rest is the caller's
	blob, next, err := intentArgs(stored)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO intents (id, item_id, state, next_attempt_at_unix, created_at_unix, payload)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		stored.GetId(), stored.GetItemId(), int32(stored.GetState()), next,
		stored.GetCreatedAt().AsTime().UnixNano(), blob,
	); err != nil {
		return fmt.Errorf("store: enqueue intent %s: %w", stored.GetId(), err)
	}
	return nil
}

func (s *sqliteStore) GetIntent(ctx context.Context, id string) (*taskcorev1.IntentRecord, error) {
	var payload []byte
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM intents WHERE id = ?`, id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get intent %s: %w", id, err)
	}
	return unmarshalIntent(payload)
}

func (s *sqliteStore) UpdateIntent(ctx context.Context, rec *taskcorev1.IntentRecord) error {
	if rec.GetId() == "" {
		return fmt.Errorf("store: intent id is required")
	}
	stored := proto.Clone(rec).(*taskcorev1.IntentRecord)
	stored.UpdatedAt = timestamppb.New(s.opts.Now())
	blob, next, err := intentArgs(stored)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE intents SET item_id = ?, state = ?, next_attempt_at_unix = ?, created_at_unix = ?, payload = ?
		 WHERE id = ?`,
		stored.GetItemId(), int32(stored.GetState()), next,
		stored.GetCreatedAt().AsTime().UnixNano(), blob, stored.GetId(),
	)
	if err != nil {
		return fmt.Errorf("store: update intent %s: %w", stored.GetId(), err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update intent %s: %w", stored.GetId(), err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListIntents filters by states (empty defaults to the live outbox) and an
// optional item id, newest first, capped by limit.
func (s *sqliteStore) ListIntents(ctx context.Context, states []taskcorev1.IntentState, itemID string, limit int) ([]*taskcorev1.IntentRecord, error) {
	if len(states) == 0 {
		states = defaultIntentStates
	}
	if limit <= 0 {
		limit = defaultIntentLimit
	}
	if limit > maxIntentLimit {
		limit = maxIntentLimit
	}

	placeholders := make([]string, len(states))
	args := make([]any, 0, len(states)+2)
	for i, st := range states {
		placeholders[i] = "?"
		args = append(args, int32(st))
	}
	query := `SELECT payload FROM intents WHERE state IN (` + strings.Join(placeholders, ", ") + `)`
	if itemID != "" {
		query += ` AND item_id = ?`
		args = append(args, itemID)
	}
	query += ` ORDER BY created_at_unix DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list intents: %w", err)
	}
	defer rows.Close()
	return scanIntents(rows)
}

// DueIntents returns QUEUED records whose next_attempt_at has arrived (or is
// unset), oldest first — the worker's feed.
func (s *sqliteStore) DueIntents(ctx context.Context, now time.Time, limit int) ([]*taskcorev1.IntentRecord, error) {
	if limit <= 0 {
		limit = defaultDueLimit
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT payload FROM intents
		 WHERE state = ? AND (next_attempt_at_unix IS NULL OR next_attempt_at_unix <= ?)
		 ORDER BY created_at_unix ASC, id ASC LIMIT ?`,
		int32(taskcorev1.IntentState_INTENT_STATE_QUEUED), now.UnixNano(), limit)
	if err != nil {
		return nil, fmt.Errorf("store: due intents: %w", err)
	}
	defer rows.Close()
	return scanIntents(rows)
}

// RequeueStaleInflight flips INFLIGHT records last touched before cutoff back
// to QUEUED (crash recovery) and reports how many. Staleness is judged by the
// record's updated_at, which lives only in the payload, so the handful of
// INFLIGHT rows are scanned in Go; UpdateIntent re-stamps updated_at and
// preserves last_error.
func (s *sqliteStore) RequeueStaleInflight(ctx context.Context, cutoff time.Time) (int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT payload FROM intents WHERE state = ?`,
		int32(taskcorev1.IntentState_INTENT_STATE_INFLIGHT))
	if err != nil {
		return 0, fmt.Errorf("store: requeue stale inflight: %w", err)
	}
	inflight, err := scanIntents(rows)
	rows.Close()
	if err != nil {
		return 0, fmt.Errorf("store: requeue stale inflight: %w", err)
	}
	count := 0
	for _, rec := range inflight {
		if !rec.GetUpdatedAt().AsTime().Before(cutoff) {
			continue
		}
		rec.State = taskcorev1.IntentState_INTENT_STATE_QUEUED
		if err := s.UpdateIntent(ctx, rec); err != nil {
			return count, fmt.Errorf("store: requeue intent %s: %w", rec.GetId(), err)
		}
		count++
	}
	return count, nil
}

// intentArgs marshals rec and derives its next_attempt_at_unix column value:
// NULL when next_attempt_at is unset, unix nanoseconds otherwise (the same
// convention as items' *_unix columns).
func intentArgs(rec *taskcorev1.IntentRecord) (blob []byte, next any, err error) {
	blob, err = proto.Marshal(rec)
	if err != nil {
		return nil, nil, fmt.Errorf("store: marshal intent %s: %w", rec.GetId(), err)
	}
	if t := rec.GetNextAttemptAt(); t != nil {
		next = t.AsTime().UnixNano()
	}
	return blob, next, nil
}

func unmarshalIntent(payload []byte) (*taskcorev1.IntentRecord, error) {
	rec := &taskcorev1.IntentRecord{}
	if err := proto.Unmarshal(payload, rec); err != nil {
		return nil, fmt.Errorf("store: unmarshal intent: %w", err)
	}
	return rec, nil
}

func scanIntents(rows *sql.Rows) ([]*taskcorev1.IntentRecord, error) {
	var out []*taskcorev1.IntentRecord
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		rec, err := unmarshalIntent(payload)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}
