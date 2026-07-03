package query_test

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/query"
)

// fakeExtFDS builds a descriptor set for a message type that is NOT linked
// into this binary — the exact situation of a plugin type arriving via a
// manifest: the daemon must traverse it through descriptors alone.
//
//	package fakeext.v1;
//	message Payload { google.protobuf.Timestamp when = 1; string place = 2; }
func fakeExtFDS(t *testing.T) (*descriptorpb.FileDescriptorSet, protoreflect.MessageDescriptor) {
	t.Helper()
	fdp := &descriptorpb.FileDescriptorProto{
		Name:       proto.String("fakeext/v1/payload.proto"),
		Package:    proto.String("fakeext.v1"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"google/protobuf/timestamp.proto"},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("Payload"),
			Field: []*descriptorpb.FieldDescriptorProto{
				{
					Name:     proto.String("when"),
					Number:   proto.Int32(1),
					Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
					Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
					TypeName: proto.String(".google.protobuf.Timestamp"),
					JsonName: proto.String("when"),
				},
				{
					Name:     proto.String("place"),
					Number:   proto.Int32(2),
					Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
					Type:     descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
					JsonName: proto.String("place"),
				},
			},
		}},
	}
	// Transitive imports ride along, as the SDK's DescriptorSet guarantees.
	tsFDP := protodesc.ToFileDescriptorProto(timestamppb.File_google_protobuf_timestamp_proto)
	fds := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{tsFDP, fdp}}

	files, err := protodesc.NewFiles(fds)
	if err != nil {
		t.Fatalf("building files: %v", err)
	}
	fd, err := files.FindFileByPath("fakeext/v1/payload.proto")
	if err != nil {
		t.Fatalf("finding file: %v", err)
	}
	return fds, fd.Messages().ByName("Payload")
}

func fakeExtItem(t *testing.T, md protoreflect.MessageDescriptor, when time.Time, place string) *taskcorev1.Item {
	t.Helper()
	msg := dynamicpb.NewMessage(md)
	msg.Set(md.Fields().ByName("when"), protoreflect.ValueOfMessage(timestamppb.New(when).ProtoReflect()))
	msg.Set(md.Fields().ByName("place"), protoreflect.ValueOfString(place))
	packed, err := anypb.New(msg)
	if err != nil {
		t.Fatalf("packing any: %v", err)
	}
	return &taskcorev1.Item{
		Id:   "x1",
		Kind: "fakeext.thing",
		Mirror: &taskcorev1.Mirror{
			Title: "ext item",
			Link:  &taskcorev1.ExternalLink{ConnectorInstance: "fake@t", ExternalId: "e1"},
			Data:  map[string]*anypb.Any{"p": packed},
		},
	}
}

func TestRegisterTypes(t *testing.T) {
	eng, err := query.NewEngine(func() time.Time { return time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	fds, md := fakeExtFDS(t)

	if err := eng.RegisterTypes(fds); err != nil {
		t.Fatalf("RegisterTypes: %v", err)
	}
	// Idempotent: plugin restarts re-register the same set.
	if err := eng.RegisterTypes(fds); err != nil {
		t.Fatalf("RegisterTypes twice: %v", err)
	}

	if err := eng.RegisterKind("fakeext.thing", map[string]string{
		"due": `item.mirror.data["p"].when`,
	}); err != nil {
		t.Fatalf("RegisterKind with extension binding: %v", err)
	}

	when := time.Date(2026, 8, 1, 9, 30, 0, 0, time.UTC)
	item := fakeExtItem(t, md, when, "office")

	// The virtual due resolves through the Any payload via descriptors only.
	got := eng.EffectiveDue(item)
	if got == nil || !got.Equal(when) {
		t.Fatalf("EffectiveDue through extension = %v, want %v", got, when)
	}
	// Override-wins still applies on top of extension bindings.
	override := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	item.Todo = &taskcorev1.Todo{Due: timestamppb.New(override)}
	if got := eng.EffectiveDue(item); got == nil || !got.Equal(override) {
		t.Fatalf("todo.due override = %v, want %v", got, override)
	}
	item.Todo = nil

	// Filters can traverse the extension data too.
	match, err := eng.Matcher(`item.mirror.data["p"].place == "office"`)
	if err != nil {
		t.Fatalf("Matcher over extension field: %v", err)
	}
	ok, err := match(item)
	if err != nil || !ok {
		t.Fatalf("extension filter = (%v, %v), want match", ok, err)
	}

	// The virtual `due` variable sees it as well — one query language across
	// native and plugin kinds is the whole point.
	match, err = eng.Matcher(`has_due && due < timestamp("2026-08-02T00:00:00Z")`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := match(item); err != nil || !ok {
		t.Fatalf("virtual due filter = (%v, %v), want match", ok, err)
	}

	// Filters compiled before the extension keep working after it.
	pre, err := eng.Matcher(`!completed`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := pre(item); err != nil || !ok {
		t.Fatalf("pre-extension filter = (%v, %v), want match", ok, err)
	}
}

func TestRegisterTypesRejectsIncompleteSet(t *testing.T) {
	eng, err := query.NewEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	fds, _ := fakeExtFDS(t)
	fds.File = fds.File[1:] // drop the timestamp.proto dependency
	if err := eng.RegisterTypes(fds); err == nil {
		t.Fatal("descriptor set missing transitive imports must be rejected")
	}
}
