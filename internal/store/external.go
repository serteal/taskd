package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
	taskcorev1 "todoapp/gen/taskcore/v1"
)

// External-identity lookups and persisted plugin manifests (phase 2). The
// sync engine reconciles snapshots by (connector_instance, external_id);
// manifests are persisted so kinds outlive their plugins.

func (s *sqliteStore) GetItemByExternal(ctx context.Context, instance, externalID string) (*taskcorev1.Item, error) {
	if instance == "" || externalID == "" {
		return nil, fmt.Errorf("store: instance and external id are required")
	}
	var blob []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT blob FROM items WHERE connector_instance = ? AND external_id = ?`,
		instance, externalID).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get by external: %w", err)
	}
	item := &taskcorev1.Item{}
	if err := proto.Unmarshal(blob, item); err != nil {
		return nil, fmt.Errorf("store: unmarshal external item: %w", err)
	}
	return item, nil
}

func (s *sqliteStore) ListInstanceItems(ctx context.Context, instance string) ([]*taskcorev1.Item, error) {
	if instance == "" {
		return nil, fmt.Errorf("store: instance is required")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT blob FROM items WHERE connector_instance = ? ORDER BY id`, instance)
	if err != nil {
		return nil, fmt.Errorf("store: list instance items: %w", err)
	}
	defer rows.Close()
	var items []*taskcorev1.Item
	for rows.Next() {
		var blob []byte
		if err := rows.Scan(&blob); err != nil {
			return nil, fmt.Errorf("store: scan instance item: %w", err)
		}
		item := &taskcorev1.Item{}
		if err := proto.Unmarshal(blob, item); err != nil {
			return nil, fmt.Errorf("store: unmarshal instance item: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *sqliteStore) SaveManifest(ctx context.Context, m *pluginv1.Manifest) error {
	if m.GetName() == "" {
		return fmt.Errorf("store: manifest name is required")
	}
	payload, err := proto.Marshal(m)
	if err != nil {
		return fmt.Errorf("store: marshal manifest: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO manifests (plugin, version, payload, stored_at_unix) VALUES (?, ?, ?, ?)
		 ON CONFLICT(plugin) DO UPDATE SET version = excluded.version, payload = excluded.payload, stored_at_unix = excluded.stored_at_unix`,
		m.GetName(), m.GetVersion(), payload, s.opts.Now().UnixNano())
	if err != nil {
		return fmt.Errorf("store: save manifest: %w", err)
	}
	return nil
}

func (s *sqliteStore) GetManifest(ctx context.Context, plugin string) (*pluginv1.Manifest, error) {
	var payload []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT payload FROM manifests WHERE plugin = ?`, plugin).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get manifest: %w", err)
	}
	m := &pluginv1.Manifest{}
	if err := proto.Unmarshal(payload, m); err != nil {
		return nil, fmt.Errorf("store: unmarshal manifest: %w", err)
	}
	return m, nil
}

func (s *sqliteStore) ListManifests(ctx context.Context) ([]*pluginv1.Manifest, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM manifests ORDER BY plugin`)
	if err != nil {
		return nil, fmt.Errorf("store: list manifests: %w", err)
	}
	defer rows.Close()
	var out []*pluginv1.Manifest
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("store: scan manifest: %w", err)
		}
		m := &pluginv1.Manifest{}
		if err := proto.Unmarshal(payload, m); err != nil {
			return nil, fmt.Errorf("store: unmarshal manifest: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
