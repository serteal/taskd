package feed

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

// TestRevertRoundtrip is the lockstep guarantee between Diff and
// BeforeImage: for a corpus of before/after pairs spanning every compared
// field, reverting Diff's changes on the after-image must reproduce the
// before-image exactly (modulo the differ's ignored bookkeeping fields,
// which we hold constant). If someone teaches Diff a new field without
// teaching revert, the completeness guard catches the differ half and this
// test catches the reverter half.
func TestRevertRoundtrip(t *testing.T) {
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ts := func(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t) }
	anyv := &anypb.Any{TypeUrl: "type.googleapis.com/x.Y", Value: []byte{1, 2, 3}}

	pairs := []struct {
		name          string
		before, after *taskcorev1.Item
	}{
		{
			name:   "promotion from bare mirror",
			before: &taskcorev1.Item{Id: "a", Kind: "k", Mirror: &taskcorev1.Mirror{Title: "m"}},
			after: &taskcorev1.Item{Id: "a", Kind: "k", Mirror: &taskcorev1.Mirror{Title: "m"},
				Todo: &taskcorev1.Todo{Labels: []string{"x"}, Project: "p"}},
		},
		{
			name:   "bare promotion (presence only)",
			before: &taskcorev1.Item{Id: "a"},
			after:  &taskcorev1.Item{Id: "a", Todo: &taskcorev1.Todo{}},
		},
		{
			name: "completion with reason",
			before: &taskcorev1.Item{Id: "a", Todo: &taskcorev1.Todo{
				TitleOverride: "t", Due: ts(base.Add(48 * time.Hour)),
			}},
			after: &taskcorev1.Item{Id: "a", Todo: &taskcorev1.Todo{
				TitleOverride: "t", Due: ts(base.Add(48 * time.Hour)),
				Completed: true, CompletedAt: ts(base), CompletedReason: "wontdo",
			}},
		},
		{
			name: "every mirror field moves",
			before: &taskcorev1.Item{Id: "a", Mirror: &taskcorev1.Mirror{
				Title: "old", State: "open",
				Link: &taskcorev1.ExternalLink{ConnectorInstance: "c@1", ExternalId: "e1", ExternalUrl: "u1", Etag: "g1"},
				Data: map[string]*anypb.Any{"p": anyv},
			}},
			after: &taskcorev1.Item{Id: "a", Mirror: &taskcorev1.Mirror{
				Title: "new", State: "merged", Stale: true, MissingSince: ts(base),
				Link: &taskcorev1.ExternalLink{ConnectorInstance: "c@1", ExternalId: "e2", ExternalUrl: "u2", Etag: "g2"},
			}},
		},
		{
			name: "relations change",
			before: &taskcorev1.Item{Id: "a", Relations: []*taskcorev1.Relation{
				{Type: taskcorev1.RelationType_RELATION_TYPE_BLOCKS, TargetId: "x"},
			}},
			after: &taskcorev1.Item{Id: "a", Relations: []*taskcorev1.Relation{
				{Type: taskcorev1.RelationType_RELATION_TYPE_INSTANCE_OF, TargetId: "y"},
				{Type: taskcorev1.RelationType_RELATION_TYPE_BLOCKS, TargetId: "x"},
			}},
		},
		{
			name: "snooze and due cleared",
			before: &taskcorev1.Item{Id: "a", Todo: &taskcorev1.Todo{
				TitleOverride: "t", Due: ts(base), SnoozedUntil: ts(base.Add(time.Hour)), Note: "n",
			}},
			after: &taskcorev1.Item{Id: "a", Todo: &taskcorev1.Todo{TitleOverride: "t2"}},
		},
	}

	for _, p := range pairs {
		t.Run(p.name, func(t *testing.T) {
			changes, err := Diff(p.before, p.after)
			if err != nil {
				t.Fatalf("Diff: %v", err)
			}
			if changes == nil {
				t.Fatal("test pair produced no changes; pick a real pair")
			}
			ev := &taskcorev1.Event{
				Type:    taskcorev1.ChangeType_CHANGE_TYPE_UPDATED,
				Changes: changes,
				Item:    p.after,
			}
			got, err := BeforeImage(ev)
			if err != nil {
				t.Fatalf("BeforeImage: %v", err)
			}
			if !proto.Equal(got, p.before) {
				t.Fatalf("roundtrip mismatch:\n got  %v\n want %v", got, p.before)
			}
		})
	}
}

func TestBeforeImageCreated(t *testing.T) {
	got, err := BeforeImage(&taskcorev1.Event{
		Type: taskcorev1.ChangeType_CHANGE_TYPE_CREATED,
		Item: &taskcorev1.Item{Id: "a"},
	})
	if err != nil || got != nil {
		t.Fatalf("CREATED before-image = (%v, %v), want (nil, nil)", got, err)
	}
}

func TestBeforeImageUnknownPath(t *testing.T) {
	_, err := BeforeImage(&taskcorev1.Event{
		Type:    taskcorev1.ChangeType_CHANGE_TYPE_UPDATED,
		Changes: []*taskcorev1.FieldChange{{Path: "not.a.field"}},
		Item:    &taskcorev1.Item{},
	})
	if err == nil {
		t.Fatal("unknown path must error loudly: differ and reverter drifting is a bug")
	}
}
