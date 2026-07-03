package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

// Rule storage (phase 3). Position drives evaluation order; SaveRule with
// position 0 appends after the current maximum so config order is stable by
// default and explicitly reorderable.

func (s *sqliteStore) SaveRule(ctx context.Context, rule *taskcorev1.Rule) error {
	if rule.GetName() == "" {
		return fmt.Errorf("store: rule name is required")
	}
	payload, err := proto.Marshal(rule)
	if err != nil {
		return fmt.Errorf("store: marshal rule: %w", err)
	}
	pos := rule.GetPosition()
	if pos == 0 {
		if err := s.db.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(position), 0) + 1 FROM rules WHERE name != ?`, rule.GetName(),
		).Scan(&pos); err != nil {
			return fmt.Errorf("store: next rule position: %w", err)
		}
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO rules (name, position, payload) VALUES (?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET position = excluded.position, payload = excluded.payload`,
		rule.GetName(), pos, payload)
	if err != nil {
		return fmt.Errorf("store: save rule: %w", err)
	}
	return nil
}

func (s *sqliteStore) GetRule(ctx context.Context, name string) (*taskcorev1.Rule, error) {
	var payload []byte
	var pos int32
	err := s.db.QueryRowContext(ctx,
		`SELECT payload, position FROM rules WHERE name = ?`, name).Scan(&payload, &pos)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get rule: %w", err)
	}
	return unmarshalRule(payload, pos)
}

// ListRules returns rules in evaluation order (position, then name).
func (s *sqliteStore) ListRules(ctx context.Context) ([]*taskcorev1.Rule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload, position FROM rules ORDER BY position, name`)
	if err != nil {
		return nil, fmt.Errorf("store: list rules: %w", err)
	}
	defer rows.Close()
	var out []*taskcorev1.Rule
	for rows.Next() {
		var payload []byte
		var pos int32
		if err := rows.Scan(&payload, &pos); err != nil {
			return nil, fmt.Errorf("store: scan rule: %w", err)
		}
		r, err := unmarshalRule(payload, pos)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *sqliteStore) DeleteRule(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM rules WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("store: delete rule: %w", err)
	}
	return nil
}

func unmarshalRule(payload []byte, pos int32) (*taskcorev1.Rule, error) {
	r := &taskcorev1.Rule{}
	if err := proto.Unmarshal(payload, r); err != nil {
		return nil, fmt.Errorf("store: unmarshal rule: %w", err)
	}
	r.Position = pos // the column is authoritative (append-assigned)
	return r, nil
}

// GetMeta returns ("", nil) for absent keys — callers treat empty as unset.
func (s *sqliteStore) GetMeta(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store: get meta %q: %w", key, err)
	}
	return v, nil
}

func (s *sqliteStore) SetMeta(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO meta (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("store: set meta %q: %w", key, err)
	}
	return nil
}
