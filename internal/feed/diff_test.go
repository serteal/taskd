package feed

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

// ignoredItemFields are the bookkeeping fields Diff deliberately skips.
var ignoredItemFields = map[string]bool{
	"id":              true,
	"kind":            true,
	"mirror_revision": true,
	"todo_revision":   true,
	"created_at":      true,
	"updated_at":      true,
}

// TestDiffCoversEveryItemLeaf is the completeness guard: it enumerates every
// leaf path of Item via protoreflect (per the differ's granularity rules,
// minus the ignore list), sets each leaf to a non-zero value on an otherwise
// empty item, and asserts Diff reports exactly that path. A field added to
// item.proto fails this test until Diff learns it.
func TestDiffCoversEveryItemLeaf(t *testing.T) {
	paths := leafPaths(t, "", (&taskcorev1.Item{}).ProtoReflect().Descriptor(), ignoredItemFields)
	if len(paths) < 15 {
		t.Fatalf("enumerated only %d leaf paths (%v); enumeration is broken", len(paths), paths)
	}
	for _, path := range paths {
		after := &taskcorev1.Item{}
		populateLeaf(t, after.ProtoReflect(), strings.Split(path, "."))
		changes, err := Diff(&taskcorev1.Item{}, after)
		if err != nil {
			t.Fatalf("Diff after setting %s: %v", path, err)
		}
		var got []string
		for _, c := range changes {
			got = append(got, c.GetPath())
		}
		if len(changes) != 1 || changes[0].GetPath() != path {
			t.Errorf("field %q: Diff reported %v — the differ has no comparator for it; teach Diff about this field", path, got)
		}
	}
}

// leafPaths enumerates diffable leaf paths: repeated and map fields are one
// leaf each; singular non-well-known messages are descended into; everything
// else (scalars and well-known types like Timestamp) is a leaf.
func leafPaths(t *testing.T, prefix string, md protoreflect.MessageDescriptor, ignore map[string]bool) []string {
	t.Helper()
	var out []string
	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		name := string(fd.Name())
		if prefix == "" && ignore[name] {
			continue
		}
		full := name
		if prefix != "" {
			full = prefix + "." + name
		}
		switch {
		case fd.IsList(), fd.IsMap():
			out = append(out, full)
		case fd.Kind() == protoreflect.MessageKind &&
			!strings.HasPrefix(string(fd.Message().FullName()), "google.protobuf."):
			out = append(out, leafPaths(t, full, fd.Message(), nil)...)
		default:
			out = append(out, full)
		}
	}
	return out
}

// populateLeaf sets the leaf at parts to a non-zero value, creating
// intermediate messages along the way.
func populateLeaf(t *testing.T, m protoreflect.Message, parts []string) {
	t.Helper()
	fd := m.Descriptor().Fields().ByName(protoreflect.Name(parts[0]))
	if fd == nil {
		t.Fatalf("no field %q in %s", parts[0], m.Descriptor().FullName())
	}
	if len(parts) > 1 {
		populateLeaf(t, m.Mutable(fd).Message(), parts[1:])
		return
	}
	switch {
	case fd.IsList():
		list := m.Mutable(fd).List()
		switch fd.Kind() {
		case protoreflect.MessageKind:
			list.Append(list.NewElement())
		case protoreflect.StringKind:
			list.Append(protoreflect.ValueOfString("x"))
		default:
			t.Fatalf("guard cannot populate list of %s at %s — teach it", fd.Kind(), fd.FullName())
		}
	case fd.IsMap():
		if fd.MapKey().Kind() != protoreflect.StringKind {
			t.Fatalf("guard cannot populate map keyed by %s at %s — teach it", fd.MapKey().Kind(), fd.FullName())
		}
		mp := m.Mutable(fd).Map()
		mp.Set(protoreflect.ValueOfString("k").MapKey(), mp.NewValue())
	case fd.Kind() == protoreflect.MessageKind:
		if fd.Message().FullName() != "google.protobuf.Timestamp" {
			t.Fatalf("guard cannot populate message leaf %s (%s) — teach it", fd.FullName(), fd.Message().FullName())
		}
		ts := timestamppb.New(time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC))
		m.Set(fd, protoreflect.ValueOfMessage(ts.ProtoReflect()))
	case fd.Kind() == protoreflect.StringKind:
		m.Set(fd, protoreflect.ValueOfString("x"))
	case fd.Kind() == protoreflect.BoolKind:
		m.Set(fd, protoreflect.ValueOfBool(true))
	case fd.Kind() == protoreflect.BytesKind:
		m.Set(fd, protoreflect.ValueOfBytes([]byte{1}))
	case fd.Kind() == protoreflect.EnumKind:
		m.Set(fd, protoreflect.ValueOfEnum(1))
	case fd.Kind() == protoreflect.Int32Kind, fd.Kind() == protoreflect.Sint32Kind, fd.Kind() == protoreflect.Sfixed32Kind:
		m.Set(fd, protoreflect.ValueOfInt32(1))
	case fd.Kind() == protoreflect.Int64Kind, fd.Kind() == protoreflect.Sint64Kind, fd.Kind() == protoreflect.Sfixed64Kind:
		m.Set(fd, protoreflect.ValueOfInt64(1))
	case fd.Kind() == protoreflect.Uint32Kind, fd.Kind() == protoreflect.Fixed32Kind:
		m.Set(fd, protoreflect.ValueOfUint32(1))
	case fd.Kind() == protoreflect.Uint64Kind, fd.Kind() == protoreflect.Fixed64Kind:
		m.Set(fd, protoreflect.ValueOfUint64(1))
	case fd.Kind() == protoreflect.FloatKind:
		m.Set(fd, protoreflect.ValueOfFloat32(1))
	case fd.Kind() == protoreflect.DoubleKind:
		m.Set(fd, protoreflect.ValueOfFloat64(1))
	default:
		t.Fatalf("guard cannot populate %s of kind %s — teach it", fd.FullName(), fd.Kind())
	}
}

// Value construction helpers for golden expectations.

func strV(s string) *structpb.Value { return structpb.NewStringValue(s) }
func boolV(b bool) *structpb.Value  { return structpb.NewBoolValue(b) }
func nullV() *structpb.Value        { return structpb.NewNullValue() }
func listV(ss ...string) *structpb.Value {
	vals := make([]*structpb.Value, 0, len(ss))
	for _, s := range ss {
		vals = append(vals, structpb.NewStringValue(s))
	}
	return structpb.NewListValue(&structpb.ListValue{Values: vals})
}

// jsonV builds a Value from plain Go data (for relation/mirror.data goldens).
func jsonV(t *testing.T, v any) *structpb.Value {
	t.Helper()
	pv, err := structpb.NewValue(v)
	if err != nil {
		t.Fatalf("structpb.NewValue: %v", err)
	}
	return pv
}

func change(path string, oldV, newV *structpb.Value) *taskcorev1.FieldChange {
	return &taskcorev1.FieldChange{Path: path, OldValue: oldV, NewValue: newV}
}

func assertChanges(t *testing.T, got, want []*taskcorev1.FieldChange) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d changes, want %d\n got: %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if !proto.Equal(got[i], want[i]) {
			t.Errorf("change[%d]:\n got: %v\nwant: %v", i, got[i], want[i])
		}
	}
}

func ts(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t) }

func TestDiffGolden(t *testing.T) {
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	halfSec := time.Date(2026, 1, 2, 3, 4, 5, 500_000_000, time.UTC)

	cases := []struct {
		name          string
		before, after *taskcorev1.Item
		want          []*taskcorev1.FieldChange
	}{
		{
			name:   "identical items produce nil",
			before: &taskcorev1.Item{Id: "a", Kind: "task", Todo: &taskcorev1.Todo{Note: "n"}},
			after:  &taskcorev1.Item{Id: "a", Kind: "task", Todo: &taskcorev1.Todo{Note: "n"}},
			want:   nil,
		},
		{
			name: "bookkeeping-only differences are ignored",
			before: &taskcorev1.Item{
				Id: "a", Kind: "task", MirrorRevision: 1, TodoRevision: 2,
				CreatedAt: ts(base), UpdatedAt: ts(base),
				Todo: &taskcorev1.Todo{Note: "n"},
			},
			after: &taskcorev1.Item{
				Id: "b", Kind: "linear.issue", MirrorRevision: 9, TodoRevision: 9,
				CreatedAt: ts(base.Add(time.Hour)), UpdatedAt: ts(base.Add(time.Hour)),
				Todo: &taskcorev1.Todo{Note: "n"},
			},
			want: nil,
		},
		{
			name:   "empty todo appearing is a no-op",
			before: &taskcorev1.Item{Id: "a"},
			after:  &taskcorev1.Item{Id: "a", Todo: &taskcorev1.Todo{}},
			want:   nil,
		},
		{
			name:   "todo appears reporting each set leaf",
			before: &taskcorev1.Item{Id: "a"},
			after: &taskcorev1.Item{Id: "a", Todo: &taskcorev1.Todo{
				Labels:  []string{"urgent"},
				Project: "work/reviews",
				Note:    "look",
			}},
			want: []*taskcorev1.FieldChange{
				change("todo.labels", listV(), listV("urgent")),
				change("todo.note", strV(""), strV("look")),
				change("todo.project", strV(""), strV("work/reviews")),
			},
		},
		{
			name:   "complete with timestamp and reason",
			before: &taskcorev1.Item{Id: "a", Todo: &taskcorev1.Todo{}},
			after: &taskcorev1.Item{Id: "a", Todo: &taskcorev1.Todo{
				Completed:       true,
				CompletedAt:     ts(halfSec),
				CompletedReason: "wontdo",
			}},
			want: []*taskcorev1.FieldChange{
				change("todo.completed", boolV(false), boolV(true)),
				change("todo.completed_at", nullV(), strV("2026-01-02T03:04:05.500Z")),
				change("todo.completed_reason", strV(""), strV("wontdo")),
			},
		},
		{
			name: "uncomplete clears timestamp",
			before: &taskcorev1.Item{Id: "a", Todo: &taskcorev1.Todo{
				Completed:   true,
				CompletedAt: ts(base),
			}},
			after: &taskcorev1.Item{Id: "a", Todo: &taskcorev1.Todo{}},
			want: []*taskcorev1.FieldChange{
				change("todo.completed", boolV(true), boolV(false)),
				change("todo.completed_at", strV("2026-01-02T03:04:05Z"), nullV()),
			},
		},
		{
			name:   "labels change as a whole list",
			before: &taskcorev1.Item{Id: "a", Todo: &taskcorev1.Todo{Labels: []string{"a", "b"}}},
			after:  &taskcorev1.Item{Id: "a", Todo: &taskcorev1.Todo{Labels: []string{"a", "c"}}},
			want: []*taskcorev1.FieldChange{
				change("todo.labels", listV("a", "b"), listV("a", "c")),
			},
		},
		{
			name:   "mirror state change",
			before: &taskcorev1.Item{Id: "a", Mirror: &taskcorev1.Mirror{State: "open"}},
			after:  &taskcorev1.Item{Id: "a", Mirror: &taskcorev1.Mirror{State: "merged"}},
			want: []*taskcorev1.FieldChange{
				change("mirror.state", strV("open"), strV("merged")),
			},
		},
		{
			name: "link etag change",
			before: &taskcorev1.Item{Id: "a", Mirror: &taskcorev1.Mirror{
				Link: &taskcorev1.ExternalLink{ConnectorInstance: "github@work", ExternalId: "42", Etag: "e1"},
			}},
			after: &taskcorev1.Item{Id: "a", Mirror: &taskcorev1.Mirror{
				Link: &taskcorev1.ExternalLink{ConnectorInstance: "github@work", ExternalId: "42", Etag: "e2"},
			}},
			want: []*taskcorev1.FieldChange{
				change("mirror.link.etag", strV("e1"), strV("e2")),
			},
		},
		{
			name: "relations change as a whole list",
			before: &taskcorev1.Item{Id: "a", Relations: []*taskcorev1.Relation{
				{Type: taskcorev1.RelationType_RELATION_TYPE_PARENT_OF, TargetId: "x"},
			}},
			after: &taskcorev1.Item{Id: "a", Relations: []*taskcorev1.Relation{
				{Type: taskcorev1.RelationType_RELATION_TYPE_PARENT_OF, TargetId: "x"},
				{Type: taskcorev1.RelationType_RELATION_TYPE_BLOCKS, TargetId: "y"},
			}},
			want: []*taskcorev1.FieldChange{
				change("relations",
					jsonV(t, []any{
						map[string]any{"type": "RELATION_TYPE_PARENT_OF", "targetId": "x"},
					}),
					jsonV(t, []any{
						map[string]any{"type": "RELATION_TYPE_PARENT_OF", "targetId": "x"},
						map[string]any{"type": "RELATION_TYPE_BLOCKS", "targetId": "y"},
					}),
				),
			},
		},
		{
			name: "mirror data change as a whole map",
			before: &taskcorev1.Item{Id: "a", Mirror: &taskcorev1.Mirror{
				Data: map[string]*anypb.Any{
					"issue": {TypeUrl: "type.googleapis.com/x.Issue", Value: []byte{1, 2}},
				},
			}},
			after: &taskcorev1.Item{Id: "a", Mirror: &taskcorev1.Mirror{
				Data: map[string]*anypb.Any{
					"issue": {TypeUrl: "type.googleapis.com/x.Issue", Value: []byte{1, 3}},
				},
			}},
			want: []*taskcorev1.FieldChange{
				change("mirror.data",
					jsonV(t, map[string]any{
						"issue": map[string]any{"type_url": "type.googleapis.com/x.Issue", "value_b64": "AQI="},
					}),
					jsonV(t, map[string]any{
						"issue": map[string]any{"type_url": "type.googleapis.com/x.Issue", "value_b64": "AQM="},
					}),
				),
			},
		},
		{
			name: "several changes at once come back sorted",
			before: &taskcorev1.Item{
				Id:     "a",
				Mirror: &taskcorev1.Mirror{State: "open"},
				Todo:   &taskcorev1.Todo{Labels: []string{"pr"}},
			},
			after: &taskcorev1.Item{
				Id:     "a",
				Mirror: &taskcorev1.Mirror{State: "merged"},
				Todo:   &taskcorev1.Todo{Completed: true, Labels: []string{"pr", "done"}},
				Relations: []*taskcorev1.Relation{
					{Type: taskcorev1.RelationType_RELATION_TYPE_RELATES_TO, TargetId: "z"},
				},
			},
			want: []*taskcorev1.FieldChange{
				change("mirror.state", strV("open"), strV("merged")),
				change("relations",
					jsonV(t, []any{}),
					jsonV(t, []any{
						map[string]any{"type": "RELATION_TYPE_RELATES_TO", "targetId": "z"},
					}),
				),
				change("todo.completed", boolV(false), boolV(true)),
				change("todo.labels", listV("pr"), listV("pr", "done")),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Diff(tc.before, tc.after)
			if err != nil {
				t.Fatalf("Diff: %v", err)
			}
			if tc.want == nil {
				if got != nil {
					t.Fatalf("want nil changes, got %v", got)
				}
				return
			}
			assertChanges(t, got, tc.want)
		})
	}
}

// TestDiffNilItems locks in that fully-nil items diff cleanly (absent
// messages are zero-valued at every level).
func TestDiffNilItems(t *testing.T) {
	got, err := Diff(nil, nil)
	if err != nil {
		t.Fatalf("Diff(nil, nil): %v", err)
	}
	if got != nil {
		t.Fatalf("Diff(nil, nil) = %v, want nil", got)
	}
	got, err = Diff(nil, &taskcorev1.Item{Todo: &taskcorev1.Todo{Note: "n"}})
	if err != nil {
		t.Fatalf("Diff(nil, item): %v", err)
	}
	assertChanges(t, got, []*taskcorev1.FieldChange{
		change("todo.note", strV(""), strV("n")),
	})
}
