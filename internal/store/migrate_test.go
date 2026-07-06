package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	taskpb "github.com/serteal/taskd/gen/task"
)

// readUserVersion opens a fresh connection to the file at path and reports its
// PRAGMA user_version — the on-disk schema version, independent of any Store.
func readUserVersion(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	defer db.Close()
	v, err := schemaVersion(context.Background(), db)
	if err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	return v
}

// legacyDB hand-builds a database predating user_version: it runs the given
// migrations' statements directly (never stamping user_version, which stays 0)
// and returns the path.
func legacyDB(t *testing.T, upto int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	for _, m := range migrations {
		if m.version > upto {
			break
		}
		for _, stmt := range m.stmts {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				t.Fatalf("legacy stmt %q: %v", stmt, err)
			}
		}
	}
	if v := readUserVersion(t, path); v != 0 {
		t.Fatalf("legacy db user_version = %d, want 0 (unversioned)", v)
	}
	return path
}

func TestMigrateFreshDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	s, err := Open(context.Background(), path, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	if got := readUserVersion(t, path); got != latestVersion() {
		t.Errorf("fresh db version = %d, want latest %d", got, latestVersion())
	}
	// The latest schema is fully present: a create/get roundtrip (which reads
	// and writes user_data) works.
	task := mustCreate(t, s, "hello")
	if _, _, err := s.Update(context.Background(), task.GetId(), 0, func(*taskpb.Task) error { return nil }); err != nil {
		t.Fatalf("Update on fresh db: %v", err)
	}
}

func TestMigrateLegacyWithoutUserData(t *testing.T) {
	path := legacyDB(t, 1) // version-1 schema, no user_data column, user_version 0
	ctx := context.Background()

	// Seed a row directly, as a real legacy database would already carry.
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx,
		"INSERT INTO tasks (id, title, notes, revision, created_ms, updated_ms) VALUES ('legacy1', 'old task', '', 1, 1000, 1000)"); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	raw.Close()

	s, err := Open(ctx, path, nil)
	if err != nil {
		t.Fatalf("Open legacy db: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	if got := readUserVersion(t, path); got != latestVersion() {
		t.Errorf("migrated version = %d, want latest %d", got, latestVersion())
	}
	// The pre-existing row survived migration and is readable.
	got, err := s.Get(ctx, "legacy1")
	if err != nil {
		t.Fatalf("Get legacy row: %v", err)
	}
	if got.GetTitle() != "old task" || got.GetUserData() != nil {
		t.Errorf("legacy row = %v, want title 'old task' and no user_data", got)
	}
	// The added column is writable: a normal create still works.
	mustCreate(t, s, "fresh after migrate")
}

func TestMigrateLegacyWithUserData(t *testing.T) {
	path := legacyDB(t, 2) // version-2 schema (user_data present) but user_version 0
	ctx := context.Background()

	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx,
		"INSERT INTO tasks (id, title, notes, revision, created_ms, updated_ms) VALUES ('legacy2', 'kept', '', 1, 1000, 1000)"); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	raw.Close()

	s, err := Open(ctx, path, nil)
	if err != nil {
		t.Fatalf("Open legacy db: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	if got := readUserVersion(t, path); got != latestVersion() {
		t.Errorf("bootstrapped version = %d, want latest %d", got, latestVersion())
	}
	if got, err := s.Get(ctx, "legacy2"); err != nil || got.GetTitle() != "kept" {
		t.Errorf("legacy row = %v, err = %v, want title 'kept'", got, err)
	}
}

// TestMigrateV3Columns: on both a fresh DB and a v2 legacy DB, the v3
// recurrence and parent_id columns end up present and usable.
func TestMigrateV3Columns(t *testing.T) {
	ctx := context.Background()

	t.Run("fresh", func(t *testing.T) {
		s, _ := newTestStore(t)
		parent := mustCreate(t, s, "parent")
		child, err := s.Create(ctx, "child", "", nil, nil, "FREQ=DAILY", parent.GetId())
		if err != nil {
			t.Fatalf("Create recurring child: %v", err)
		}
		if child.GetRecurrence() != "FREQ=DAILY" || child.GetParentId() != parent.GetId() {
			t.Errorf("v3 fields not stored: %v", child)
		}
	})

	t.Run("from v2 legacy", func(t *testing.T) {
		path := legacyDB(t, 2) // v2 schema, no recurrence/parent_id, user_version 0
		s, err := Open(ctx, path, nil)
		if err != nil {
			t.Fatalf("Open legacy v2: %v", err)
		}
		t.Cleanup(func() { s.Close() })
		if got := readUserVersion(t, path); got != latestVersion() {
			t.Errorf("migrated version = %d, want latest %d", got, latestVersion())
		}
		// The columns added by migration 3 are writable.
		task, err := s.Create(ctx, "recurring", "", nil, nil, "FREQ=WEEKLY", "")
		if err != nil {
			t.Fatalf("Create after v2->v3 migrate: %v", err)
		}
		got, err := s.Get(ctx, task.GetId())
		if err != nil || got.GetRecurrence() != "FREQ=WEEKLY" {
			t.Errorf("recurrence column not usable after migrate: %v, err %v", got, err)
		}
	})
}

func TestMigrateReopenIsNoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	ctx := context.Background()

	s1, err := Open(ctx, path, nil)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	task := mustCreate(t, s1, "persist")
	s1.Close()

	s2, err := Open(ctx, path, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { s2.Close() })
	if got := readUserVersion(t, path); got != latestVersion() {
		t.Errorf("reopened version = %d, want latest %d", got, latestVersion())
	}
	if _, err := s2.Get(ctx, task.GetId()); err != nil {
		t.Errorf("task lost across reopen: %v", err)
	}
}

// TestMigrateHalfCreatedLegacyRefused: a database with a tasks table but no
// task_labels and user_version 0 is a half-created DB, not a genuine legacy
// one. Adopting it as v1 would skip the migration that creates task_labels and
// never repair it, so Open must refuse loudly instead.
func TestMigrateHalfCreatedLegacyRefused(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "broken.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	// tasks present, task_labels absent, user_version left at 0.
	if _, err := db.ExecContext(ctx,
		"CREATE TABLE tasks (id TEXT PRIMARY KEY, title TEXT NOT NULL, revision INTEGER NOT NULL, created_ms INTEGER NOT NULL, updated_ms INTEGER NOT NULL)"); err != nil {
		t.Fatalf("create tasks: %v", err)
	}
	db.Close()
	if v := readUserVersion(t, path); v != 0 {
		t.Fatalf("user_version = %d, want 0 (unversioned)", v)
	}

	if _, err := Open(ctx, path, nil); err == nil || !strings.Contains(err.Error(), "missing task_labels") {
		t.Errorf("Open half-created db err = %v, want a 'missing task_labels' refusal", err)
	}
}

func TestMigrateNewerVersionRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	ctx := context.Background()

	s, err := Open(ctx, path, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.Close()

	// Stamp a version beyond anything this build knows.
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if err := setSchemaVersion(ctx, raw, latestVersion()+1); err != nil {
		t.Fatalf("bump version: %v", err)
	}
	raw.Close()

	if _, err := Open(ctx, path, nil); err == nil || !strings.Contains(err.Error(), "newer taskd") {
		t.Errorf("Open on newer-version db err = %v, want a 'newer taskd' error", err)
	}
}
