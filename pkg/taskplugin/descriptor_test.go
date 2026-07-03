package taskplugin_test

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
	"todoapp/pkg/taskplugin"
)

func TestDescriptorSet(t *testing.T) {
	set := taskplugin.DescriptorSet(&pluginv1.Manifest{})

	counts := make(map[string]int)
	index := make(map[string]int) // file name → position in the set
	for i, f := range set.GetFile() {
		counts[f.GetName()]++
		index[f.GetName()] = i
	}

	// The message's own file, its taskcore/v1 imports (schema_service, and
	// rule.proto via Manifest.rule_templates), and the well-known
	// google/protobuf deps must each appear exactly once.
	for _, want := range []string{
		"taskcore/plugin/v1/plugin.proto",
		"taskcore/v1/schema_service.proto",
		"taskcore/v1/rule.proto",
		"google/protobuf/descriptor.proto",
		"google/protobuf/duration.proto",
		"google/protobuf/struct.proto",
	} {
		if counts[want] != 1 {
			t.Errorf("file %q appears %d times, want exactly 1", want, counts[want])
		}
	}
	// No file appears more than once, and nothing outside the transitive
	// closure sneaks in.
	for name, n := range counts {
		if n != 1 {
			t.Errorf("file %q appears %d times, want 1", name, n)
		}
	}
	if len(set.GetFile()) != 6 {
		t.Errorf("set has %d files, want 6: %v", len(set.GetFile()), fileNames(set.GetFile()))
	}

	// Every dependency is in the set and precedes its importer.
	for _, f := range set.GetFile() {
		for _, dep := range f.GetDependency() {
			di, ok := index[dep]
			if !ok {
				t.Errorf("file %q imports %q, which is missing from the set", f.GetName(), dep)
				continue
			}
			if di >= index[f.GetName()] {
				t.Errorf("dependency %q comes after importer %q", dep, f.GetName())
			}
		}
	}

	// Deterministic: a second call yields an identical set.
	again := taskplugin.DescriptorSet(&pluginv1.Manifest{})
	if !proto.Equal(set, again) {
		t.Error("DescriptorSet is not deterministic across calls")
	}
}

func fileNames(files []*descriptorpb.FileDescriptorProto) []string {
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.GetName()
	}
	return names
}
