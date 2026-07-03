// Package query is the CEL query engine: it compiles user filters into a
// SQL pushdown over the store's indexed columns plus a per-item residual
// evaluator, evaluates kind-registered facet bindings to expose virtual
// fields (effective due), and extracts the indexed columns for the store.
package query

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/store"
)

// Engine owns the single CEL environment, the injected clock, and the
// per-kind virtual-field bindings. Safe for concurrent use.
type Engine struct {
	env *cel.Env
	now func() time.Time

	mu sync.RWMutex
	// kinds maps kind -> virtual field name -> compiled binding program
	// (input variable: item).
	kinds map[string]map[string]cel.Program
}

// NewEngine builds the CEL environment (generated proto types plus the
// convenience variables available in filters) and pre-registers the native
// "task" kind's due binding. now is the injected clock used for the `now`
// and `snoozed` filter variables; nil defaults to time.Now.
func NewEngine(now func() time.Time) (*Engine, error) {
	if now == nil {
		now = time.Now
	}
	env, err := cel.NewEnv(
		cel.Types(&taskcorev1.Item{}),
		cel.Variable("item", cel.ObjectType("taskcore.v1.Item")),
		cel.Variable("completed", cel.BoolType),
		cel.Variable("labels", cel.ListType(cel.StringType)),
		cel.Variable("project", cel.StringType),
		cel.Variable("kind", cel.StringType),
		cel.Variable("state", cel.StringType),
		cel.Variable("stale", cel.BoolType),
		cel.Variable("title", cel.StringType),
		cel.Variable("due", cel.TimestampType),
		cel.Variable("has_due", cel.BoolType),
		cel.Variable("snoozed_until", cel.TimestampType),
		cel.Variable("snoozed", cel.BoolType),
		cel.Variable("now", cel.TimestampType),
	)
	if err != nil {
		return nil, fmt.Errorf("query: building CEL environment: %w", err)
	}
	e := &Engine{
		env:   env,
		now:   now,
		kinds: make(map[string]map[string]cel.Program),
	}
	// The native kind's schedulable facet: due is bound straight to the
	// user layer (override-wins is then a no-op for native tasks).
	if err := e.RegisterKind("task", map[string]string{"due": "item.todo.due"}); err != nil {
		return nil, err
	}
	return e, nil
}

// celEnv returns the current environment. RegisterTypes swaps it; programs
// compiled against an older environment remain valid.
func (e *Engine) celEnv() *cel.Env {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.env
}

// RegisterTypes extends the CEL environment with a plugin's descriptor set
// (Manifest.KindRegistration.types), so filters and bindings can traverse
// typed extension payloads in mirror.data — Any values resolve by type_url
// against these descriptors even though the daemon never linked the types.
// Files already known to the environment (well-known types, taskcore) are
// skipped; re-registering the same set is a no-op, which makes this safe to
// call on every plugin start.
func (e *Engine) RegisterTypes(fds *descriptorpb.FileDescriptorSet) error {
	if fds == nil || len(fds.GetFile()) == 0 {
		return nil
	}
	files, err := protodesc.NewFiles(fds)
	if err != nil {
		return fmt.Errorf("query: invalid descriptor set (must include transitive imports): %w", err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	var fresh []protoreflect.FileDescriptor
	var rangeErr error
	files.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		pkg := string(fd.Package())
		if strings.HasPrefix(pkg, "google.protobuf") || strings.HasPrefix(pkg, "taskcore.") {
			return true // already in the base environment
		}
		// Skip files whose types the env already knows (idempotent restarts).
		known := true
		msgs := fd.Messages()
		for i := 0; i < msgs.Len(); i++ {
			if _, found := e.env.CELTypeProvider().FindStructType(string(msgs.Get(i).FullName())); !found {
				known = false
				break
			}
		}
		if known && msgs.Len() > 0 {
			return true
		}
		fresh = append(fresh, fd)
		return true
	})
	if rangeErr != nil {
		return rangeErr
	}
	if len(fresh) == 0 {
		return nil
	}
	ext, err := e.env.Extend(cel.TypeDescs(files))
	if err != nil {
		return fmt.Errorf("query: registering plugin types: %w", err)
	}
	// cel-go resolves Any payloads through the GLOBAL proto registry
	// (anypb.UnmarshalNew), not the env's descriptor DB — so descriptor-only
	// types must also be registered globally as dynamic types or every
	// mirror.data traversal fails with "proto: not found". Idempotent
	// (skip anything already linked or registered); additive for the
	// process lifetime, which is exactly what a runtime schema registry is.
	for _, fd := range fresh {
		if err := registerDynamicTypes(fd.Messages()); err != nil {
			return fmt.Errorf("query: registering dynamic types for %s: %w", fd.Path(), err)
		}
	}
	e.env = ext
	return nil
}

func registerDynamicTypes(msgs protoreflect.MessageDescriptors) error {
	for i := 0; i < msgs.Len(); i++ {
		md := msgs.Get(i)
		if _, err := protoregistry.GlobalTypes.FindMessageByName(md.FullName()); err == nil {
			continue // linked or previously registered
		}
		if err := protoregistry.GlobalTypes.RegisterMessage(dynamicpb.NewMessageType(md)); err != nil {
			return err
		}
		if err := registerDynamicTypes(md.Messages()); err != nil { // nested types
			return err
		}
	}
	return nil
}

// RegisterKind compiles the kind's virtual-field bindings (CEL expressions
// over the single input variable `item`) and stores the programs. A "due"
// binding must produce a timestamp (or null/dyn, e.g. when reaching through
// mirror.data extensions). Re-registering a kind replaces its bindings.
// Call RegisterTypes first when the bindings reach into extension types.
func (e *Engine) RegisterKind(kind string, virtual map[string]string) error {
	env := e.celEnv()
	programs := make(map[string]cel.Program, len(virtual))
	for field, expr := range virtual {
		ast, iss := env.Compile(expr)
		if iss != nil && iss.Err() != nil {
			return fmt.Errorf("query: kind %q: binding for %q does not compile: %w", kind, field, iss.Err())
		}
		if field == "due" {
			out := ast.OutputType()
			if !out.IsExactType(cel.TimestampType) && !out.IsExactType(cel.NullType) && !out.IsExactType(cel.DynType) {
				return fmt.Errorf("query: kind %q: binding for %q must evaluate to a timestamp or null, got %s", kind, field, out)
			}
		}
		prg, err := env.Program(ast)
		if err != nil {
			return fmt.Errorf("query: kind %q: binding for %q: %w", kind, field, err)
		}
		programs[field] = prg
	}
	e.mu.Lock()
	e.kinds[kind] = programs
	e.mu.Unlock()
	return nil
}

// EffectiveDue resolves the item's effective due (override-wins): todo.due
// when set, else the kind's "due" binding evaluated over the item. A null
// or zero-timestamp binding result — the proto default when the bound
// field is absent — means no due. Binding evaluation errors and
// non-timestamp results also resolve to no due: bindings are a lens, not a
// validator.
func (e *Engine) EffectiveDue(item *taskcorev1.Item) *time.Time {
	if d := item.GetTodo().GetDue(); d != nil {
		t := d.AsTime()
		return &t
	}
	e.mu.RLock()
	prg := e.kinds[item.GetKind()]["due"]
	e.mu.RUnlock()
	if prg == nil {
		return nil
	}
	out, _, err := prg.Eval(map[string]any{"item": item})
	if err != nil {
		return nil
	}
	ts, ok := out.(types.Timestamp)
	if !ok {
		return nil // null or a non-timestamp dyn result
	}
	t := ts.Time
	if t.IsZero() || (t.Unix() == 0 && t.Nanosecond() == 0) {
		return nil // proto zero timestamp: the bound field is absent
	}
	return &t
}

// Extract derives the store's indexed columns from an item.
func (e *Engine) Extract(item *taskcorev1.Item) store.Indexed {
	todo := item.GetTodo()
	var snoozed *time.Time
	if su := todo.GetSnoozedUntil(); su != nil {
		t := su.AsTime()
		snoozed = &t
	}
	return store.Indexed{
		Completed:    todo != nil && todo.GetCompleted(),
		Project:      todo.GetProject(),
		Due:          e.EffectiveDue(item),
		SnoozedUntil: snoozed,
	}
}

// activation builds the per-item variable bindings for one evaluation.
// The convenience variables are computed here, per item, at call time.
func (e *Engine) activation(item *taskcorev1.Item, now time.Time) map[string]any {
	todo := item.GetTodo()
	mirror := item.GetMirror()

	labels := todo.GetLabels()
	if labels == nil {
		labels = []string{}
	}

	title := todo.GetTitleOverride()
	if title == "" {
		title = mirror.GetTitle()
	}

	// due is the effective (virtual) due, so `due < X` works uniformly
	// across kinds; absent maps to the proto zero timestamp, guarded by
	// has_due.
	due := &timestamppb.Timestamp{}
	hasDue := false
	if t := e.EffectiveDue(item); t != nil {
		due = timestamppb.New(*t)
		hasDue = true
	}

	snoozedUntil := todo.GetSnoozedUntil()
	// Strictly after now: an item whose snooze expires exactly now is no
	// longer snoozed.
	isSnoozed := snoozedUntil != nil && snoozedUntil.AsTime().After(now)
	if snoozedUntil == nil {
		snoozedUntil = &timestamppb.Timestamp{}
	}

	return map[string]any{
		"item":          item,
		"completed":     todo.GetCompleted(),
		"labels":        labels,
		"project":       todo.GetProject(),
		"kind":          item.GetKind(),
		"state":         mirror.GetState(),
		"stale":         mirror.GetStale(),
		"title":         title,
		"due":           due,
		"has_due":       hasDue,
		"snoozed_until": snoozedUntil,
		"snoozed":       isSnoozed,
		"now":           timestamppb.New(now),
	}
}
