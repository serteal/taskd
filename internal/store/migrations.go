package store

import (
	"database/sql"
	"fmt"
)

// migrations holds the numbered schema migrations; index i is schema version
// i+1. Each migration runs in its own transaction and is recorded in
// schema_migrations, so a partially-applied migration never marks itself
// done. Append only — never edit a shipped entry.
//
// All *_unix columns are unix nanoseconds. due_unix and snoozed_until_unix
// are NULL when the item has no due/snooze.
var migrations = []string{
	// 001: items (blob + extracted index columns), the append-only event log,
	// and saved views.
	`CREATE TABLE items (
		id TEXT PRIMARY KEY,
		kind TEXT NOT NULL,
		blob BLOB NOT NULL,
		completed INTEGER NOT NULL,
		project TEXT NOT NULL DEFAULT '',
		due_unix INTEGER,
		snoozed_until_unix INTEGER,
		created_at_unix INTEGER NOT NULL,
		updated_at_unix INTEGER NOT NULL
	);
	CREATE TABLE events (
		cursor INTEGER PRIMARY KEY AUTOINCREMENT,
		item_id TEXT NOT NULL,
		recorded_at_unix INTEGER NOT NULL,
		payload BLOB NOT NULL
	);
	CREATE TABLE views (
		name TEXT PRIMARY KEY,
		payload BLOB NOT NULL
	);
	CREATE INDEX idx_items_completed ON items(completed);
	CREATE INDEX idx_items_project ON items(project);
	CREATE INDEX idx_items_due ON items(due_unix);
	CREATE INDEX idx_items_updated_at ON items(updated_at_unix);
	CREATE INDEX idx_items_kind ON items(kind);
	CREATE INDEX idx_events_recorded_at ON events(recorded_at_unix);`,

	// 002 (phase 2): external identity columns so the sync engine can
	// reconcile snapshots by (connector_instance, external_id), and persisted
	// plugin manifests so kinds outlive their plugins (descriptors are the
	// schema registry — DESIGN.md §2.5).
	`ALTER TABLE items ADD COLUMN connector_instance TEXT NOT NULL DEFAULT '';
	ALTER TABLE items ADD COLUMN external_id TEXT NOT NULL DEFAULT '';
	CREATE INDEX idx_items_external ON items(connector_instance, external_id);
	CREATE TABLE manifests (
		plugin TEXT PRIMARY KEY,
		version TEXT NOT NULL,
		payload BLOB NOT NULL,
		stored_at_unix INTEGER NOT NULL
	);`,
}

// applyMigrations brings the database to the latest schema version.
func applyMigrations(db *sql.DB) error {
	if _, err := db.Exec(
		`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`,
	); err != nil {
		return fmt.Errorf("store: create schema_migrations: %w", err)
	}
	for i, stmt := range migrations {
		version := i + 1
		var n int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version,
		).Scan(&n); err != nil {
			return fmt.Errorf("store: check migration %d: %w", version, err)
		}
		if n > 0 {
			continue
		}
		if err := applyMigration(db, version, stmt); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration(db *sql.DB, version int, stmt string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("store: begin migration %d: %w", version, err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(stmt); err != nil {
		return fmt.Errorf("store: apply migration %d: %w", version, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO schema_migrations (version) VALUES (?)`, version,
	); err != nil {
		return fmt.Errorf("store: record migration %d: %w", version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit migration %d: %w", version, err)
	}
	return nil
}
