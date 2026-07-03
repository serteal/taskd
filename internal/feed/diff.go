package feed

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"sort"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

// Diff computes the field-level changes between two versions of an item.
// It is the system's edge detector: every UPDATED event's FieldChanges come
// from here, so rules' "became X" semantics and sync echo silencing are
// exactly as good as this function.
//
// Semantics:
//   - Paths are proto field names relative to Item ("todo.completed",
//     "mirror.link.etag", "relations").
//   - Granularity is leaf scalars/timestamps inside todo, mirror, and
//     mirror.link; whole-list for repeated fields (todo.labels, relations);
//     whole-map for mirror.data.
//   - Bookkeeping fields (id, kind, mirror_revision, todo_revision,
//     created_at, updated_at) are ignored entirely.
//   - An absent Todo/Mirror/ExternalLink message is treated as zero-valued,
//     so setting todo on a previously todo-less item reports each newly-set
//     leaf, never one opaque "todo" change.
//
// Value encoding (google.protobuf.Value):
//   - strings and bools as-is;
//   - timestamps as RFC 3339 strings per protojson conventions, NullValue
//     when unset;
//   - repeated string as a ListValue of strings;
//   - relations as a ListValue of protojson-encoded objects;
//   - mirror.data as an object mapping key to
//     {"type_url": ..., "value_b64": base64 of Any.value} — Any payloads are
//     deliberately not protojson-encoded, their types may be unregistered.
//
// The result is sorted by path. A nil result means no semantic change
// (proto.Equal-level for the compared fields): the caller must write no
// event, which is what silences sync echoes by value.
//
// This is an explicit field walk, not generic protoreflect recursion —
// ~20 comparisons, clear and reviewable. The completeness guard in
// diff_test.go fails the build the moment item.proto grows a field this
// walk does not cover.
func Diff(before, after *taskcorev1.Item) ([]*taskcorev1.FieldChange, error) {
	d := &differ{}

	bt, at := before.GetTodo(), after.GetTodo()
	d.cmpBool("todo.completed", bt.GetCompleted(), at.GetCompleted())
	d.cmpTimestamp("todo.completed_at", bt.GetCompletedAt(), at.GetCompletedAt())
	d.cmpString("todo.completed_reason", bt.GetCompletedReason(), at.GetCompletedReason())
	d.cmpStringList("todo.labels", bt.GetLabels(), at.GetLabels())
	d.cmpString("todo.project", bt.GetProject(), at.GetProject())
	d.cmpTimestamp("todo.due", bt.GetDue(), at.GetDue())
	d.cmpTimestamp("todo.snoozed_until", bt.GetSnoozedUntil(), at.GetSnoozedUntil())
	d.cmpString("todo.title_override", bt.GetTitleOverride(), at.GetTitleOverride())
	d.cmpString("todo.note", bt.GetNote(), at.GetNote())

	bm, am := before.GetMirror(), after.GetMirror()
	bl, al := bm.GetLink(), am.GetLink()
	d.cmpString("mirror.link.connector_instance", bl.GetConnectorInstance(), al.GetConnectorInstance())
	d.cmpString("mirror.link.external_id", bl.GetExternalId(), al.GetExternalId())
	d.cmpString("mirror.link.external_url", bl.GetExternalUrl(), al.GetExternalUrl())
	d.cmpString("mirror.link.etag", bl.GetEtag(), al.GetEtag())
	d.cmpTimestamp("mirror.link.last_synced_at", bl.GetLastSyncedAt(), al.GetLastSyncedAt())
	d.cmpString("mirror.title", bm.GetTitle(), am.GetTitle())
	d.cmpString("mirror.state", bm.GetState(), am.GetState())
	d.cmpAnyMap("mirror.data", bm.GetData(), am.GetData())
	d.cmpBool("mirror.stale", bm.GetStale(), am.GetStale())
	d.cmpTimestamp("mirror.missing_since", bm.GetMissingSince(), am.GetMissingSince())

	d.cmpRelations("relations", before.GetRelations(), after.GetRelations())

	if d.err != nil {
		return nil, d.err
	}
	if len(d.changes) == 0 {
		return nil, nil
	}
	sort.Slice(d.changes, func(i, j int) bool { return d.changes[i].Path < d.changes[j].Path })
	return d.changes, nil
}

// differ accumulates changes and the first encoding error.
type differ struct {
	changes []*taskcorev1.FieldChange
	err     error
}

func (d *differ) add(path string, oldV, newV *structpb.Value) {
	d.changes = append(d.changes, &taskcorev1.FieldChange{
		Path:     path,
		OldValue: oldV,
		NewValue: newV,
	})
}

func (d *differ) cmpString(path, before, after string) {
	if before == after {
		return
	}
	d.add(path, structpb.NewStringValue(before), structpb.NewStringValue(after))
}

func (d *differ) cmpBool(path string, before, after bool) {
	if before == after {
		return
	}
	d.add(path, structpb.NewBoolValue(before), structpb.NewBoolValue(after))
}

func (d *differ) cmpTimestamp(path string, before, after *timestamppb.Timestamp) {
	if d.err != nil || proto.Equal(before, after) {
		return
	}
	oldV, err := timestampValue(before)
	if err != nil {
		d.err = fmt.Errorf("feed: diff %s: %w", path, err)
		return
	}
	newV, err := timestampValue(after)
	if err != nil {
		d.err = fmt.Errorf("feed: diff %s: %w", path, err)
		return
	}
	d.add(path, oldV, newV)
}

func (d *differ) cmpStringList(path string, before, after []string) {
	if slices.Equal(before, after) {
		return
	}
	d.add(path, stringListValue(before), stringListValue(after))
}

func (d *differ) cmpRelations(path string, before, after []*taskcorev1.Relation) {
	if d.err != nil || relationsEqual(before, after) {
		return
	}
	oldV, err := relationsValue(before)
	if err != nil {
		d.err = fmt.Errorf("feed: diff %s: %w", path, err)
		return
	}
	newV, err := relationsValue(after)
	if err != nil {
		d.err = fmt.Errorf("feed: diff %s: %w", path, err)
		return
	}
	d.add(path, oldV, newV)
}

func (d *differ) cmpAnyMap(path string, before, after map[string]*anypb.Any) {
	if anyMapEqual(before, after) {
		return
	}
	d.add(path, anyMapValue(before), anyMapValue(after))
}

// timestampValue encodes a timestamp the way protojson would (RFC 3339,
// UTC, 0/3/6/9 fractional digits); unset becomes NullValue.
func timestampValue(ts *timestamppb.Timestamp) (*structpb.Value, error) {
	if ts == nil {
		return structpb.NewNullValue(), nil
	}
	b, err := protojson.Marshal(ts)
	if err != nil {
		return nil, err
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return structpb.NewStringValue(s), nil
}

func stringListValue(ss []string) *structpb.Value {
	vals := make([]*structpb.Value, 0, len(ss))
	for _, s := range ss {
		vals = append(vals, structpb.NewStringValue(s))
	}
	return structpb.NewListValue(&structpb.ListValue{Values: vals})
}

func relationsEqual(a, b []*taskcorev1.Relation) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !proto.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

// relationsValue encodes relations as a ListValue of protojson objects
// (Relation contains only well-known scalar/enum fields, so protojson is
// safe here, unlike for Any payloads).
func relationsValue(rels []*taskcorev1.Relation) (*structpb.Value, error) {
	vals := make([]*structpb.Value, 0, len(rels))
	for _, r := range rels {
		b, err := protojson.Marshal(r)
		if err != nil {
			return nil, err
		}
		v := &structpb.Value{}
		if err := v.UnmarshalJSON(b); err != nil {
			return nil, err
		}
		vals = append(vals, v)
	}
	return structpb.NewListValue(&structpb.ListValue{Values: vals}), nil
}

func anyMapEqual(a, b map[string]*anypb.Any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok || !proto.Equal(av, bv) {
			return false
		}
	}
	return true
}

// anyMapValue encodes mirror.data as an object mapping each key to
// {"type_url": ..., "value_b64": ...}. The Any payload bytes ride as base64
// rather than protojson because extension types may not be registered in
// this process — the encoding must never depend on the registry.
func anyMapValue(m map[string]*anypb.Any) *structpb.Value {
	fields := make(map[string]*structpb.Value, len(m))
	for k, a := range m {
		fields[k] = structpb.NewStructValue(&structpb.Struct{Fields: map[string]*structpb.Value{
			"type_url":  structpb.NewStringValue(a.GetTypeUrl()),
			"value_b64": structpb.NewStringValue(base64.StdEncoding.EncodeToString(a.GetValue())),
		}})
	}
	return structpb.NewStructValue(&structpb.Struct{Fields: fields})
}
