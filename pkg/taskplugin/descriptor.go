package taskplugin

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// DescriptorSet builds the FileDescriptorSet for Manifest.KindRegistration
// types: the files declaring the given messages plus all transitive imports,
// each file exactly once. Order is deterministic — post-order over the
// import graph, so every file's dependencies precede it. Registered
// descriptors outlive the plugin: the core persists them so stored data
// stays renderable after uninstall (DESIGN.md §10).
func DescriptorSet(msgs ...proto.Message) *descriptorpb.FileDescriptorSet {
	set := &descriptorpb.FileDescriptorSet{}
	seen := make(map[string]bool)
	var add func(fd protoreflect.FileDescriptor)
	add = func(fd protoreflect.FileDescriptor) {
		if seen[fd.Path()] {
			return
		}
		seen[fd.Path()] = true
		imports := fd.Imports()
		for i := 0; i < imports.Len(); i++ {
			add(imports.Get(i).FileDescriptor)
		}
		set.File = append(set.File, protodesc.ToFileDescriptorProto(fd))
	}
	for _, m := range msgs {
		add(m.ProtoReflect().Descriptor().ParentFile())
	}
	return set
}
