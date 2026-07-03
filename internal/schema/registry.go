// Package schema is the kind/facet registry. It persists plugin manifests
// (kinds outlive their plugins), enforces additive-only evolution of plugin
// types (a new plugin version whose schema would make stored data unreadable
// is refused at load), and projects registered kinds into the query engine
// (types + virtual-field bindings) and SchemaService.
package schema

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/query"
	"todoapp/internal/rules"
	"todoapp/internal/store"
)

// NativeKind is the built-in kind; everything else arrives via manifests.
const NativeKind = "task"

type Registry struct {
	st  store.Store
	eng *query.Engine

	mu    sync.RWMutex
	kinds map[string]*taskcorev1.KindInfo // merged view for SchemaService
	owner map[string]string               // kind -> plugin name (collision guard)
}

func NewRegistry(st store.Store, eng *query.Engine) *Registry {
	r := &Registry{
		st:    st,
		eng:   eng,
		kinds: make(map[string]*taskcorev1.KindInfo),
		owner: make(map[string]string),
	}
	// The native kind mirrors the engine's built-in registration.
	r.kinds[NativeKind] = &taskcorev1.KindInfo{
		Kind: NativeKind,
		Facets: []*taskcorev1.FacetBinding{
			{Facet: "completable"},
			{Facet: "schedulable", Bindings: map[string]string{"due": "item.todo.due"}},
		},
	}
	r.owner[NativeKind] = ""
	return r
}

// Load restores persisted manifests at daemon start: stored items stay
// renderable, filterable, and exportable whether or not their plugin still
// exists.
func (r *Registry) Load(ctx context.Context) error {
	manifests, err := r.st.ListManifests(ctx)
	if err != nil {
		return fmt.Errorf("schema: loading manifests: %w", err)
	}
	for _, m := range manifests {
		if err := r.apply(m); err != nil {
			return fmt.Errorf("schema: restoring manifest %q: %w", m.GetName(), err)
		}
	}
	return nil
}

// RegisterManifest is the plugin host's gate, run between GetManifest and
// Configure. It refuses breaking changes against the stored manifest, then
// persists and applies the new one. Idempotent for an unchanged manifest.
func (r *Registry) RegisterManifest(ctx context.Context, m *pluginv1.Manifest) error {
	if m.GetName() == "" {
		return errors.New("schema: manifest has no name")
	}
	stored, err := r.st.GetManifest(ctx, m.GetName())
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if stored != nil {
		if err := checkAdditive(stored, m); err != nil {
			return fmt.Errorf("schema: plugin %q v%s is not additive over stored v%s (refusing to load — stored items must stay readable): %w",
				m.GetName(), m.GetVersion(), stored.GetVersion(), err)
		}
	}
	if err := r.apply(m); err != nil {
		return err
	}
	// Templates are proposals, but broken proposals are bugs: a template
	// that fails validation would only ever fail LATER, in the user's face,
	// at `rule template apply`. Refuse it here, in the plugin author's face.
	// Validated after apply so template conditions may reference the
	// manifest's own kinds/types.
	for _, tpl := range m.GetRuleTemplates() {
		if err := rules.Validate(r.eng, tpl); err != nil {
			return fmt.Errorf("schema: plugin %q ships a broken rule template: %w", m.GetName(), err)
		}
	}
	return r.st.SaveManifest(ctx, m)
}

// apply registers the manifest's types and kinds into the engine and the
// merged view.
func (r *Registry) apply(m *pluginv1.Manifest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, k := range m.GetKinds() {
		kind := k.GetKind()
		if kind == "" || kind == NativeKind {
			return fmt.Errorf("schema: plugin %q registers invalid kind %q", m.GetName(), kind)
		}
		if owner, taken := r.owner[kind]; taken && owner != m.GetName() {
			return fmt.Errorf("schema: kind %q already owned by plugin %q", kind, owner)
		}
	}
	for _, k := range m.GetKinds() {
		if err := r.eng.RegisterTypes(k.GetTypes()); err != nil {
			return err
		}
		virtual, err := flattenBindings(k)
		if err != nil {
			return err
		}
		if err := r.eng.RegisterKind(k.GetKind(), virtual); err != nil {
			return err
		}
		r.kinds[k.GetKind()] = &taskcorev1.KindInfo{Kind: k.GetKind(), Facets: k.GetFacets()}
		r.owner[k.GetKind()] = m.GetName()
	}
	return nil
}

// Kinds returns the merged kind list, stable order, for SchemaService.
func (r *Registry) Kinds() []*taskcorev1.KindInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*taskcorev1.KindInfo, 0, len(r.kinds))
	for _, k := range r.kinds {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GetKind() < out[j].GetKind() })
	return out
}

// flattenBindings merges a kind's facet bindings into one virtual-field map;
// two facets binding the same field is a manifest bug, not a merge.
func flattenBindings(k *pluginv1.KindRegistration) (map[string]string, error) {
	virtual := make(map[string]string)
	for _, f := range k.GetFacets() {
		for field, expr := range f.GetBindings() {
			if prev, dup := virtual[field]; dup && prev != expr {
				return nil, fmt.Errorf("schema: kind %q binds virtual field %q twice", k.GetKind(), field)
			}
			virtual[field] = expr
		}
	}
	return virtual, nil
}

// checkAdditive enforces the evolution contract: every kind, message, and
// field of the stored manifest must survive into the new one with identical
// numbers, names, kinds, and cardinality. Additions are free; anything else
// would orphan stored blobs. (A genuinely new shape means a new kind name.)
func checkAdditive(stored, next *pluginv1.Manifest) error {
	nextKinds := make(map[string]*pluginv1.KindRegistration, len(next.GetKinds()))
	for _, k := range next.GetKinds() {
		nextKinds[k.GetKind()] = k
	}
	for _, sk := range stored.GetKinds() {
		nk, ok := nextKinds[sk.GetKind()]
		if !ok {
			return fmt.Errorf("kind %q was removed", sk.GetKind())
		}
		if err := checkTypesAdditive(sk, nk); err != nil {
			return fmt.Errorf("kind %q: %w", sk.GetKind(), err)
		}
	}
	return nil
}

func checkTypesAdditive(stored, next *pluginv1.KindRegistration) error {
	if stored.GetTypes() == nil || len(stored.GetTypes().GetFile()) == 0 {
		return nil
	}
	storedFiles, err := protodesc.NewFiles(stored.GetTypes())
	if err != nil {
		return fmt.Errorf("stored descriptor set unreadable: %w", err)
	}
	if next.GetTypes() == nil {
		return errors.New("descriptor set was removed")
	}
	nextFiles, err := protodesc.NewFiles(next.GetTypes())
	if err != nil {
		return fmt.Errorf("new descriptor set unreadable: %w", err)
	}
	nextMsgs := collectMessages(nextFiles)

	var problem error
	storedFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		msgs := collectMessagesOf(fd.Messages(), nil)
		for _, sm := range msgs {
			nm, ok := nextMsgs[sm.FullName()]
			if !ok {
				problem = fmt.Errorf("message %s was removed", sm.FullName())
				return false
			}
			sf := sm.Fields()
			for i := 0; i < sf.Len(); i++ {
				f := sf.Get(i)
				nf := nm.Fields().ByNumber(f.Number())
				switch {
				case nf == nil:
					problem = fmt.Errorf("field %s (#%d) was removed", f.FullName(), f.Number())
				case nf.Name() != f.Name():
					problem = fmt.Errorf("field #%d of %s renamed %s → %s (breaks stored bindings and exports)", f.Number(), sm.FullName(), f.Name(), nf.Name())
				case nf.Kind() != f.Kind():
					problem = fmt.Errorf("field %s changed kind %s → %s", f.FullName(), f.Kind(), nf.Kind())
				case nf.Cardinality() != f.Cardinality():
					problem = fmt.Errorf("field %s changed cardinality", f.FullName())
				}
				if problem != nil {
					return false
				}
			}
		}
		return true
	})
	return problem
}

func collectMessages(files *protoregistry.Files) map[protoreflect.FullName]protoreflect.MessageDescriptor {
	out := make(map[protoreflect.FullName]protoreflect.MessageDescriptor)
	files.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		for _, m := range collectMessagesOf(fd.Messages(), nil) {
			out[m.FullName()] = m
		}
		return true
	})
	return out
}

func collectMessagesOf(msgs protoreflect.MessageDescriptors, acc []protoreflect.MessageDescriptor) []protoreflect.MessageDescriptor {
	for i := 0; i < msgs.Len(); i++ {
		m := msgs.Get(i)
		acc = append(acc, m)
		acc = collectMessagesOf(m.Messages(), acc)
	}
	return acc
}
