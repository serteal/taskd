package feed

import (
	"encoding/base64"
	"fmt"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

// BeforeImage reconstructs the item as it was before the event, by
// reverting the event's FieldChanges on the after-image. It is the exact
// inverse of Diff for every compared field (the roundtrip property test in
// revert_test.go holds the two in lockstep) — which is what gives
// edge-triggered rules a real before-state to evaluate `became` conditions
// against, without storing two snapshots per event.
//
// CREATED events have no before-image: callers treat the condition as false
// by definition. DELETED events revert to the last state trivially (no
// changes). Bookkeeping fields the differ ignores (revisions, updated_at,
// mirror.link.last_synced_at) keep their after values — rules cannot
// reference a truthful before for them, and must not need to.
func BeforeImage(ev *taskcorev1.Event) (*taskcorev1.Item, error) {
	if ev.GetType() == taskcorev1.ChangeType_CHANGE_TYPE_CREATED {
		return nil, nil
	}
	item := proto.Clone(ev.GetItem()).(*taskcorev1.Item)
	// Presence reverts run LAST: leaf reverts materialize their parent
	// message as a side effect, so "todo: absent before" must win after all
	// todo.* leaves have been processed — otherwise a reverted leaf would
	// resurrect an empty message where there was none.
	var presence []*taskcorev1.FieldChange
	for _, c := range ev.GetChanges() {
		if p := c.GetPath(); p == "todo" || p == "mirror" {
			presence = append(presence, c)
			continue
		}
		if err := revert(item, c); err != nil {
			return nil, fmt.Errorf("feed: reverting %s: %w", c.GetPath(), err)
		}
	}
	for _, c := range presence {
		if err := revert(item, c); err != nil {
			return nil, fmt.Errorf("feed: reverting %s: %w", c.GetPath(), err)
		}
	}
	return item, nil
}

func revert(item *taskcorev1.Item, c *taskcorev1.FieldChange) error {
	old := c.GetOldValue()
	switch p := c.GetPath(); p {
	// Presence: old=false means the message was absent before.
	case "todo":
		if !old.GetBoolValue() {
			item.Todo = nil
		} else if item.Todo == nil {
			item.Todo = &taskcorev1.Todo{}
		}
	case "mirror":
		if !old.GetBoolValue() {
			item.Mirror = nil
		} else if item.Mirror == nil {
			item.Mirror = &taskcorev1.Mirror{}
		}

	case "todo.completed":
		todo(item).Completed = old.GetBoolValue()
	case "todo.completed_at":
		ts, err := valueTimestamp(old)
		if err != nil {
			return err
		}
		todo(item).CompletedAt = ts
	case "todo.completed_reason":
		todo(item).CompletedReason = old.GetStringValue()
	case "todo.labels":
		todo(item).Labels = valueStrings(old)
	case "todo.project":
		todo(item).Project = old.GetStringValue()
	case "todo.due":
		ts, err := valueTimestamp(old)
		if err != nil {
			return err
		}
		todo(item).Due = ts
	case "todo.snoozed_until":
		ts, err := valueTimestamp(old)
		if err != nil {
			return err
		}
		todo(item).SnoozedUntil = ts
	case "todo.title_override":
		todo(item).TitleOverride = old.GetStringValue()
	case "todo.note":
		todo(item).Note = old.GetStringValue()

	case "mirror.title":
		mirror(item).Title = old.GetStringValue()
	case "mirror.state":
		mirror(item).State = old.GetStringValue()
	case "mirror.stale":
		mirror(item).Stale = old.GetBoolValue()
	case "mirror.pinned":
		mirror(item).Pinned = old.GetBoolValue()
	case "mirror.missing_since":
		ts, err := valueTimestamp(old)
		if err != nil {
			return err
		}
		mirror(item).MissingSince = ts
	case "mirror.data":
		data, err := valueAnyMap(old)
		if err != nil {
			return err
		}
		mirror(item).Data = data
	case "mirror.link.connector_instance":
		link(item).ConnectorInstance = old.GetStringValue()
	case "mirror.link.external_id":
		link(item).ExternalId = old.GetStringValue()
	case "mirror.link.external_url":
		link(item).ExternalUrl = old.GetStringValue()
	case "mirror.link.etag":
		link(item).Etag = old.GetStringValue()

	case "relations":
		rels, err := valueRelations(old)
		if err != nil {
			return err
		}
		item.Relations = rels

	default:
		return fmt.Errorf("unknown change path %q (differ and reverter out of sync)", p)
	}
	return nil
}

func todo(item *taskcorev1.Item) *taskcorev1.Todo {
	if item.Todo == nil {
		item.Todo = &taskcorev1.Todo{}
	}
	return item.Todo
}

func mirror(item *taskcorev1.Item) *taskcorev1.Mirror {
	if item.Mirror == nil {
		item.Mirror = &taskcorev1.Mirror{}
	}
	return item.Mirror
}

func link(item *taskcorev1.Item) *taskcorev1.ExternalLink {
	m := mirror(item)
	if m.Link == nil {
		m.Link = &taskcorev1.ExternalLink{}
	}
	return m.Link
}

// valueTimestamp decodes the differ's timestamp encoding: RFC 3339 string,
// or null for unset.
func valueTimestamp(v *structpb.Value) (*timestamppb.Timestamp, error) {
	if v == nil || v.GetKind() == nil {
		return nil, nil
	}
	if _, isNull := v.GetKind().(*structpb.Value_NullValue); isNull {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339Nano, v.GetStringValue())
	if err != nil {
		return nil, fmt.Errorf("timestamp %q: %w", v.GetStringValue(), err)
	}
	return timestamppb.New(t), nil
}

func valueStrings(v *structpb.Value) []string {
	vals := v.GetListValue().GetValues()
	if len(vals) == 0 {
		return nil
	}
	out := make([]string, 0, len(vals))
	for _, e := range vals {
		out = append(out, e.GetStringValue())
	}
	return out
}

// valueRelations decodes the differ's relations encoding: a list of
// protojson-encoded Relation objects.
func valueRelations(v *structpb.Value) ([]*taskcorev1.Relation, error) {
	vals := v.GetListValue().GetValues()
	if len(vals) == 0 {
		return nil, nil
	}
	out := make([]*taskcorev1.Relation, 0, len(vals))
	for _, e := range vals {
		raw, err := e.MarshalJSON()
		if err != nil {
			return nil, err
		}
		rel := &taskcorev1.Relation{}
		if err := protojson.Unmarshal(raw, rel); err != nil {
			return nil, fmt.Errorf("relation: %w", err)
		}
		out = append(out, rel)
	}
	return out, nil
}

// valueAnyMap decodes the differ's mirror.data encoding:
// key -> {"type_url": ..., "value_b64": ...}.
func valueAnyMap(v *structpb.Value) (map[string]*anypb.Any, error) {
	fields := v.GetStructValue().GetFields()
	if len(fields) == 0 {
		return nil, nil
	}
	out := make(map[string]*anypb.Any, len(fields))
	for k, e := range fields {
		obj := e.GetStructValue().GetFields()
		payload, err := base64.StdEncoding.DecodeString(obj["value_b64"].GetStringValue())
		if err != nil {
			return nil, fmt.Errorf("data[%s]: %w", k, err)
		}
		out[k] = &anypb.Any{TypeUrl: obj["type_url"].GetStringValue(), Value: payload}
	}
	return out, nil
}
