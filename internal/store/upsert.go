package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/oklog/ulid/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	taskpb "todoapp/gen/task"
)

// UpsertResult reports what one UpsertExternal batch did.
type UpsertResult struct {
	Created, Updated, Unchanged, Deleted int32
	Changed                              []*taskpb.Task // final state of created+updated tasks, in batch order
	DeletedIDs                           []string       // ids removed by full_snapshot pruning
}

// UpsertExternal reconciles one source's external tasks in a single
// transaction, keyed by (source, external_ref). The source owns title,
// due_time, completed_time, and external_data — the batch overwrites them.
// The user owns notes and labels — never touched; applyLabels only adds.
// A stored task identical to its batch entry (including already carrying
// applyLabels) is left untouched, revision unchanged. With fullSnapshot, the
// source's tasks absent from the batch are deleted; local tasks (source "")
// are never affected. Any validation failure rejects the whole batch.
func (s *Store) UpsertExternal(ctx context.Context, source string, batch []*taskpb.ExternalTask, applyLabels []string, fullSnapshot bool) (*UpsertResult, error) {
	if source == "" {
		return nil, fmt.Errorf("source must not be empty: %w", ErrInvalid)
	}
	applyLabels, err := normalizeLabels(applyLabels)
	if err != nil {
		return nil, err
	}
	titles := make([]string, len(batch))
	seenRefs := make(map[string]struct{}, len(batch))
	for i, et := range batch {
		ref := et.GetExternalRef()
		if ref == "" {
			return nil, fmt.Errorf("batch[%d]: external_ref must not be empty: %w", i, ErrInvalid)
		}
		if _, dup := seenRefs[ref]; dup {
			return nil, fmt.Errorf("batch[%d]: duplicate external_ref %q: %w", i, ref, ErrInvalid)
		}
		seenRefs[ref] = struct{}{}
		titles[i] = strings.TrimSpace(et.GetTitle())
		if titles[i] == "" {
			return nil, fmt.Errorf("batch[%d]: title must not be empty: %w", i, ErrInvalid)
		}
	}

	nowMs := s.now().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	res := &UpsertResult{}
	var changedIDs []string
	for i, et := range batch {
		dueMs := msOf(et.GetDueTime())
		completedMs := msOf(et.GetCompletedTime())
		dataJSON, err := marshalStruct(et.GetExternalData())
		if err != nil {
			return nil, err
		}

		var r taskRow
		err = r.scan(tx.QueryRowContext(ctx,
			"SELECT "+taskColumns+" FROM tasks WHERE source = ? AND external_ref = ?",
			source, et.GetExternalRef()))
		switch {
		case errors.Is(err, sql.ErrNoRows):
			id := ulid.Make().String()
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO tasks (id, title, notes, due_ms, completed_ms, source, external_ref, external_data, revision, created_ms, updated_ms)
				 VALUES (?, ?, '', ?, ?, ?, ?, ?, 1, ?, ?)`,
				id, titles[i], dueMs, completedMs, source, et.GetExternalRef(), dataJSON, nowMs, nowMs); err != nil {
				return nil, fmt.Errorf("insert external task: %w", err)
			}
			if err := insertLabels(ctx, tx, id, applyLabels); err != nil {
				return nil, err
			}
			res.Created++
			changedIDs = append(changedIDs, id)

		case err != nil:
			return nil, err

		default:
			existing, err := taskLabels(ctx, tx, r.id)
			if err != nil {
				return nil, err
			}
			same, err := sourceFieldsEqual(&r, titles[i], dueMs, completedMs, et.GetExternalData())
			if err != nil {
				return nil, err
			}
			missing := missingLabels(existing, applyLabels)
			if same && len(missing) == 0 {
				res.Unchanged++
				continue
			}
			if _, err := tx.ExecContext(ctx,
				"UPDATE tasks SET title = ?, due_ms = ?, completed_ms = ?, external_data = ?, revision = revision + 1, updated_ms = ? WHERE id = ?",
				titles[i], dueMs, completedMs, dataJSON, nowMs, r.id); err != nil {
				return nil, fmt.Errorf("update external task: %w", err)
			}
			if err := insertLabels(ctx, tx, r.id, missing); err != nil {
				return nil, err
			}
			res.Updated++
			changedIDs = append(changedIDs, r.id)
		}
	}

	if fullSnapshot {
		deletedIDs, err := pruneSource(ctx, tx, source, seenRefs)
		if err != nil {
			return nil, err
		}
		res.DeletedIDs = deletedIDs
		res.Deleted = int32(len(deletedIDs))
	}

	for _, id := range changedIDs {
		t, err := getTask(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		res.Changed = append(res.Changed, t)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}

// sourceFieldsEqual reports whether the stored row already matches the
// incoming source-owned fields at millisecond precision.
func sourceFieldsEqual(r *taskRow, title string, dueMs, completedMs sql.NullInt64, data *structpb.Struct) (bool, error) {
	if r.title != title || r.dueMs != dueMs || r.completedMs != completedMs {
		return false, nil
	}
	stored, err := unmarshalStruct(r.externalData)
	if err != nil {
		return false, err
	}
	return structsEqual(stored, data), nil
}

// structsEqual compares external_data by proto semantics. Nil and field-less
// structs are one canonical empty value (both persist as NULL).
func structsEqual(a, b *structpb.Struct) bool {
	if len(a.GetFields()) == 0 || len(b.GetFields()) == 0 {
		return len(a.GetFields()) == 0 && len(b.GetFields()) == 0
	}
	return proto.Equal(a, b)
}

func missingLabels(existing, want []string) []string {
	have := make(map[string]struct{}, len(existing))
	for _, l := range existing {
		have[l] = struct{}{}
	}
	var missing []string
	for _, l := range want {
		if _, ok := have[l]; !ok {
			missing = append(missing, l)
		}
	}
	return missing
}

// pruneSource deletes the source's tasks whose external_ref is not in keep,
// returning their ids in ascending order.
func pruneSource(ctx context.Context, tx *sql.Tx, source string, keep map[string]struct{}) ([]string, error) {
	query := "SELECT id FROM tasks WHERE source = ?"
	args := []any{source}
	if len(keep) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(keep)), ",")
		query += " AND external_ref NOT IN (" + ph + ")"
		for ref := range keep {
			args = append(args, ref)
		}
	}
	rows, err := tx.QueryContext(ctx, query+" ORDER BY id", args...)
	if err != nil {
		return nil, fmt.Errorf("find stale tasks: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, "DELETE FROM tasks WHERE id = ?", id); err != nil {
			return nil, fmt.Errorf("prune task: %w", err)
		}
	}
	return ids, nil
}
