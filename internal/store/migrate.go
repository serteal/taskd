package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// migration is one ordered schema step. Each is applied in its own
// transaction that also stamps PRAGMA user_version to version, so an
// interrupted upgrade rolls back whole and the recorded version never runs
// ahead of the schema it names.
type migration struct {
	version int
	stmts   []string
}

// migrations is the schema history, applied in order. Append new versions;
// never edit or reorder a shipped one. The last entry's version is the schema
// this build expects, and every database Open touches is brought up to it.
var migrations = []migration{
	{version: 1, stmts: []string{
		`CREATE TABLE tasks (
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
		`CREATE UNIQUE INDEX tasks_by_external ON tasks(source, external_ref) WHERE source <> ''`,
		`CREATE INDEX tasks_by_completed_due ON tasks(completed_ms, due_ms)`,
		`CREATE INDEX tasks_by_created ON tasks(created_ms, id)`,
		`CREATE INDEX tasks_by_updated ON tasks(updated_ms, id)`,
		`CREATE TABLE task_labels (
			task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
			label   TEXT NOT NULL,
			PRIMARY KEY (task_id, label)
		) WITHOUT ROWID`,
		`CREATE INDEX labels_by_label ON task_labels(label)`,
	}},
	{version: 2, stmts: []string{
		`ALTER TABLE tasks ADD COLUMN user_data TEXT`,
	}},
	{version: 3, stmts: []string{
		`ALTER TABLE tasks ADD COLUMN recurrence TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN parent_id TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX tasks_by_parent ON tasks(parent_id) WHERE parent_id <> ''`,
	}},
}

// latestVersion is the schema version Open migrates every database to.
func latestVersion() int { return migrations[len(migrations)-1].version }

// migrate brings db to the latest schema version. It reads PRAGMA
// user_version: a database predating versioning reports 0, so if a tasks
// table is already present its version is inferred from its columns and
// adopted (its rows are never recreated); a genuinely empty database applies
// every migration from scratch. A version newer than this build understands
// is refused rather than guessed at.
func migrate(ctx context.Context, db *sql.DB) error {
	version, err := schemaVersion(ctx, db)
	if err != nil {
		return err
	}
	if version == 0 {
		legacy, err := legacyVersion(ctx, db)
		if err != nil {
			return err
		}
		if legacy > 0 {
			if err := setSchemaVersion(ctx, db, legacy); err != nil {
				return err
			}
			version = legacy
		}
	}
	if latest := latestVersion(); version > latest {
		return fmt.Errorf("database is from a newer taskd (schema version %d, this build knows %d)", version, latest)
	}
	for _, m := range migrations {
		if m.version <= version {
			continue
		}
		if err := applyMigration(ctx, db, m); err != nil {
			return fmt.Errorf("apply migration %d: %w", m.version, err)
		}
	}
	return nil
}

// legacyVersion reports the version a pre-user_version database is already at,
// read from its columns: 0 when there is no tasks table (a fresh database with
// nothing to adopt), 2 when tasks carries a user_data column, else 1.
func legacyVersion(ctx context.Context, db *sql.DB) (int, error) {
	hasTasks, err := tableExists(ctx, db, "tasks")
	if err != nil {
		return 0, err
	}
	if !hasTasks {
		return 0, nil
	}
	// A genuine legacy database always has task_labels beside tasks — both are
	// created together in migration 1. A tasks table with no task_labels is a
	// half-created database, not a legacy one; adopting it as v1 would skip the
	// migration that creates task_labels and never repair it. Refuse loudly.
	hasLabels, err := tableExists(ctx, db, "task_labels")
	if err != nil {
		return 0, err
	}
	if !hasLabels {
		return 0, fmt.Errorf("legacy database is missing task_labels; refusing to adopt")
	}
	hasUserData, err := columnExists(ctx, db, "tasks", "user_data")
	if err != nil {
		return 0, err
	}
	if hasUserData {
		return 2, nil
	}
	return 1, nil
}

// tableExists reports whether the database has a table of the given name.
func tableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var name string
	err := db.QueryRowContext(ctx,
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect schema: %w", err)
	}
	return true, nil
}

// columnExists reports whether table has a column of the given name.
func columnExists(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	// PRAGMA takes no bind parameters; table is a fixed internal identifier.
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return false, fmt.Errorf("inspect %s columns: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		// PRAGMA table_info columns: cid, name, type, notnull, dflt_value, pk.
		var (
			cid         int
			name, typ   string
			notnull, pk int
			dflt        sql.NullString
		)
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// applyMigration runs one migration's statements and stamps user_version in
// the same transaction.
func applyMigration(ctx context.Context, db *sql.DB, m migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range m.stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if err := setSchemaVersion(ctx, tx, m.version); err != nil {
		return err
	}
	return tx.Commit()
}

func schemaVersion(ctx context.Context, q querier) (int, error) {
	var v int
	if err := q.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return v, nil
}

func setSchemaVersion(ctx context.Context, q querier, v int) error {
	// PRAGMA takes no bind parameters; v is an int, so interpolation is safe.
	if _, err := q.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", v)); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}
	return nil
}
