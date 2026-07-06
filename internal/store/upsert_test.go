package store

import (
	"context"
	"errors"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	taskpb "github.com/serteal/taskd/gen/task"
)

func mustStruct(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mustUpsert(t *testing.T, s *Store, source string, batch []*taskpb.ExternalTask, applyLabels []string, fullSnapshot bool) *UpsertResult {
	t.Helper()
	res, err := s.UpsertExternal(context.Background(), source, batch, applyLabels, fullSnapshot)
	if err != nil {
		t.Fatalf("UpsertExternal: %v", err)
	}
	return res
}

func assertCounts(t *testing.T, res *UpsertResult, created, updated, unchanged, deleted int32) {
	t.Helper()
	if res.Created != created || res.Updated != updated || res.Unchanged != unchanged || res.Deleted != deleted {
		t.Errorf("counts = created %d, updated %d, unchanged %d, deleted %d; want %d/%d/%d/%d",
			res.Created, res.Updated, res.Unchanged, res.Deleted, created, updated, unchanged, deleted)
	}
}

func TestUpsertCreate(t *testing.T) {
	s, clk := newTestStore(t)
	wantMs := clk.cur.UnixMilli()
	res := mustUpsert(t, s, "github", []*taskpb.ExternalTask{
		{ExternalRef: "pr/2", Title: "  Review PR  ", DueTime: msTs(5000), ExternalData: mustStruct(t, map[string]any{"author": "kim"})},
		{ExternalRef: "pr/1", Title: "Fix CI", CompletedTime: msTs(6000)},
	}, []string{" gh ", "gh", "review"}, false)

	assertCounts(t, res, 2, 0, 0, 0)
	if len(res.Changed) != 2 {
		t.Fatalf("Changed has %d tasks, want 2", len(res.Changed))
	}
	// Changed follows batch order, not ref or id order.
	first, second := res.Changed[0], res.Changed[1]
	if first.GetExternalRef() != "pr/2" || second.GetExternalRef() != "pr/1" {
		t.Errorf("Changed refs = %q, %q; want batch order pr/2, pr/1", first.GetExternalRef(), second.GetExternalRef())
	}
	if first.GetTitle() != "Review PR" {
		t.Errorf("title = %q, want trimmed", first.GetTitle())
	}
	if first.GetSource() != "github" || first.GetNotes() != "" || first.GetRevision() != 1 {
		t.Errorf("created task = %v; want source github, empty notes, revision 1", first)
	}
	if got, want := first.GetLabels(), []string{"gh", "review"}; !slices.Equal(got, want) {
		t.Errorf("labels = %v, want normalized applyLabels %v", got, want)
	}
	if !proto.Equal(first.GetDueTime(), msTs(5000)) {
		t.Errorf("due_time = %v", first.GetDueTime())
	}
	if !proto.Equal(first.GetExternalData(), mustStruct(t, map[string]any{"author": "kim"})) {
		t.Errorf("external_data = %v", first.GetExternalData())
	}
	if !proto.Equal(second.GetCompletedTime(), msTs(6000)) {
		t.Errorf("completed_time = %v", second.GetCompletedTime())
	}
	if !proto.Equal(first.GetCreateTime(), msTs(wantMs)) || !proto.Equal(first.GetUpdateTime(), msTs(wantMs)) {
		t.Errorf("timestamps = %v/%v, want %v", first.GetCreateTime(), first.GetUpdateTime(), msTs(wantMs))
	}

	got, err := s.Get(context.Background(), first.GetId())
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(got, first) {
		t.Errorf("stored %v != returned %v", got, first)
	}
}

func TestUpsertUpdateAndUnchanged(t *testing.T) {
	s, _ := newTestStore(t)
	batch := []*taskpb.ExternalTask{
		{ExternalRef: "e/1", Title: "same", DueTime: msTs(1000)},
		{ExternalRef: "e/2", Title: "will change"},
	}
	mustUpsert(t, s, "ics", batch, nil, false)

	// Identical batch: nothing rewritten, revisions untouched.
	res := mustUpsert(t, s, "ics", batch, nil, false)
	assertCounts(t, res, 0, 0, 2, 0)
	if len(res.Changed) != 0 {
		t.Errorf("Changed = %v, want empty", res.Changed)
	}

	// One task's source-owned field changes.
	batch[1].Title = "changed"
	res = mustUpsert(t, s, "ics", batch, nil, false)
	assertCounts(t, res, 0, 1, 1, 0)
	if len(res.Changed) != 1 || res.Changed[0].GetTitle() != "changed" {
		t.Fatalf("Changed = %v", res.Changed)
	}
	if res.Changed[0].GetRevision() != 2 {
		t.Errorf("revision = %d, want 2", res.Changed[0].GetRevision())
	}

	// Unchanged task kept revision 1 through both upserts.
	unchanged := mustList(t, s, Page{Filter: &taskpb.TaskFilter{Text: "same"}})
	if len(unchanged) != 1 || unchanged[0].GetRevision() != 1 {
		t.Errorf("unchanged task = %v, want revision 1", unchanged)
	}
}

func TestUpsertPreservesUserFields(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	res := mustUpsert(t, s, "github", []*taskpb.ExternalTask{
		{ExternalRef: "pr/1", Title: "original"},
	}, []string{"gh"}, false)
	id := res.Changed[0].GetId()

	// The user annotates the synced task.
	if _, _, err := s.Update(ctx, id, 0, func(tk *taskpb.Task) error {
		tk.Notes = "my notes"
		tk.Labels = append(tk.Labels, "mine")
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// The source retitles it; user fields must survive.
	res = mustUpsert(t, s, "github", []*taskpb.ExternalTask{
		{ExternalRef: "pr/1", Title: "retitled"},
	}, []string{"gh"}, false)
	assertCounts(t, res, 0, 1, 0, 0)
	got := res.Changed[0]
	if got.GetNotes() != "my notes" {
		t.Errorf("notes = %q, want user notes preserved", got.GetNotes())
	}
	if want := []string{"gh", "mine"}; !slices.Equal(got.GetLabels(), want) {
		t.Errorf("labels = %v, want %v", got.GetLabels(), want)
	}
	if got.GetTitle() != "retitled" {
		t.Errorf("title = %q, want source-owned overwrite", got.GetTitle())
	}
}

func TestUpsertApplyLabelsAdditive(t *testing.T) {
	s, _ := newTestStore(t)
	batch := []*taskpb.ExternalTask{{ExternalRef: "r", Title: "t"}}
	mustUpsert(t, s, "src", batch, []string{"x"}, false)

	// Same source fields, but a new applyLabel is missing → Updated.
	res := mustUpsert(t, s, "src", batch, []string{"x", "y"}, false)
	assertCounts(t, res, 0, 1, 0, 0)
	if want := []string{"x", "y"}; !slices.Equal(res.Changed[0].GetLabels(), want) {
		t.Errorf("labels = %v, want %v", res.Changed[0].GetLabels(), want)
	}
	if res.Changed[0].GetRevision() != 2 {
		t.Errorf("revision = %d, want 2", res.Changed[0].GetRevision())
	}

	// All applyLabels already present → Unchanged; subset never removes.
	res = mustUpsert(t, s, "src", batch, []string{"y"}, false)
	assertCounts(t, res, 0, 0, 1, 0)
}

func TestUpsertExternalDataProtoEquality(t *testing.T) {
	s, _ := newTestStore(t)
	batch := func(data *structpb.Struct) []*taskpb.ExternalTask {
		return []*taskpb.ExternalTask{{ExternalRef: "r", Title: "t", ExternalData: data}}
	}
	mustUpsert(t, s, "src", batch(mustStruct(t, map[string]any{
		"b": "x", "a": 1.0, "nested": map[string]any{"k1": true, "k2": []any{1.0, 2.0}},
	})), nil, false)

	// Semantically identical struct built independently → Unchanged: the
	// comparison is proto.Equal on the unmarshaled JSON, not string equality.
	res := mustUpsert(t, s, "src", batch(mustStruct(t, map[string]any{
		"nested": map[string]any{"k2": []any{1.0, 2.0}, "k1": true}, "a": 1.0, "b": "x",
	})), nil, false)
	assertCounts(t, res, 0, 0, 1, 0)

	// A real value change is detected.
	res = mustUpsert(t, s, "src", batch(mustStruct(t, map[string]any{
		"b": "x", "a": 2.0, "nested": map[string]any{"k1": true, "k2": []any{1.0, 2.0}},
	})), nil, false)
	assertCounts(t, res, 0, 1, 0, 0)

	// Dropping the struct entirely is a change; nil and unset compare equal
	// afterwards, so the follow-up is Unchanged.
	res = mustUpsert(t, s, "src", batch(nil), nil, false)
	assertCounts(t, res, 0, 1, 0, 0)
	if res.Changed[0].GetExternalData() != nil {
		t.Errorf("external_data = %v, want nil", res.Changed[0].GetExternalData())
	}
	res = mustUpsert(t, s, "src", batch(nil), nil, false)
	assertCounts(t, res, 0, 0, 1, 0)
}

func TestUpsertFullSnapshotPrune(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	res := mustUpsert(t, s, "github", []*taskpb.ExternalTask{
		{ExternalRef: "pr/1", Title: "one"},
		{ExternalRef: "pr/2", Title: "two"},
		{ExternalRef: "pr/3", Title: "three"},
	}, nil, false)
	staleIDs := []string{res.Changed[0].GetId(), res.Changed[2].GetId()}
	slices.Sort(staleIDs)

	// Other sources and local tasks must never be pruned.
	mustUpsert(t, s, "ics", []*taskpb.ExternalTask{{ExternalRef: "ev/1", Title: "event"}}, nil, false)
	local := mustCreate(t, s, "local")

	res = mustUpsert(t, s, "github", []*taskpb.ExternalTask{
		{ExternalRef: "pr/2", Title: "two"},
	}, nil, true)
	assertCounts(t, res, 0, 0, 1, 2)
	got := slices.Clone(res.DeletedIDs)
	slices.Sort(got)
	if !slices.Equal(got, staleIDs) {
		t.Errorf("DeletedIDs = %v, want %v", res.DeletedIDs, staleIDs)
	}
	for _, id := range staleIDs {
		if _, err := s.Get(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("pruned task %s still present (err %v)", id, err)
		}
	}
	remaining := titlesOf(mustList(t, s, Page{OrderBy: "title"}))
	if want := []string{"event", "local", "two"}; !slices.Equal(remaining, want) {
		t.Errorf("remaining = %v, want %v", remaining, want)
	}
	if _, err := s.Get(ctx, local.GetId()); err != nil {
		t.Errorf("local task affected by snapshot: %v", err)
	}
}

func TestUpsertEmptyBatchFullSnapshot(t *testing.T) {
	s, _ := newTestStore(t)
	mustUpsert(t, s, "src", []*taskpb.ExternalTask{
		{ExternalRef: "a", Title: "a"},
		{ExternalRef: "b", Title: "b"},
	}, nil, false)
	res := mustUpsert(t, s, "src", nil, nil, true)
	assertCounts(t, res, 0, 0, 0, 2)
	if len(res.DeletedIDs) != 2 {
		t.Errorf("DeletedIDs = %v, want 2 ids", res.DeletedIDs)
	}
	if left := mustList(t, s, Page{}); len(left) != 0 {
		t.Errorf("tasks remain after empty full snapshot: %v", titlesOf(left))
	}
}

func TestUpsertValidation(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	valid := &taskpb.ExternalTask{ExternalRef: "ok", Title: "ok"}

	tests := []struct {
		name   string
		source string
		batch  []*taskpb.ExternalTask
		labels []string
	}{
		{"empty source", "", []*taskpb.ExternalTask{valid}, nil},
		{"empty ref", "src", []*taskpb.ExternalTask{{Title: "t"}}, nil},
		{"blank title", "src", []*taskpb.ExternalTask{{ExternalRef: "r", Title: "  "}}, nil},
		{"duplicate refs", "src", []*taskpb.ExternalTask{
			{ExternalRef: "r", Title: "a"}, {ExternalRef: "r", Title: "b"},
		}, nil},
		{"blank apply label", "src", []*taskpb.ExternalTask{valid}, []string{" "}},
		// The valid entry precedes the invalid one: the whole batch must
		// still be rejected atomically.
		{"atomic rejection", "src", []*taskpb.ExternalTask{valid, {Title: "no ref"}}, nil},
	}
	for _, tc := range tests {
		if _, err := s.UpsertExternal(ctx, tc.source, tc.batch, tc.labels, false); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", tc.name, err)
		}
	}
	if tasks := mustList(t, s, Page{}); len(tasks) != 0 {
		t.Errorf("rejected batches left tasks behind: %v", titlesOf(tasks))
	}
}
