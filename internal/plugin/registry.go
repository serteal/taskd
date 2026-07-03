package plugin

import (
	"context"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
)

// Registry is the daemon's live view of running connector instances — the
// dispatch surface the intent router and LinkItem resolve through. An
// absent instance answers Unavailable, which the outbox classifies as
// transient: intents queue until the plugin is back.
type Registry struct {
	mu sync.RWMutex
	m  map[string]*Running
}

func NewRegistry() *Registry {
	return &Registry{m: make(map[string]*Running)}
}

func (g *Registry) Add(instance string, r *Running) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.m[instance] = r
}

func (g *Registry) Remove(instance string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.m, instance)
}

func (g *Registry) get(instance string) *Running {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.m[instance]
}

// Supports reports whether the instance declared this intent for the kind
// in its Configure capabilities. Unknown instances support nothing.
func (g *Registry) Supports(instance, kind string, in pluginv1.Intent) bool {
	r := g.get(instance)
	if r == nil {
		return false
	}
	for _, ki := range r.Capabilities().GetIntents() {
		if ki.GetKind() != kind {
			continue
		}
		for _, have := range ki.GetIntents() {
			if have == in {
				return true
			}
		}
	}
	return false
}

func (g *Registry) HandleIntent(ctx context.Context, instance string, req *pluginv1.HandleIntentRequest) (*pluginv1.RemoteItem, error) {
	r := g.get(instance)
	if r == nil {
		return nil, status.Errorf(codes.Unavailable, "connector instance %q is not running", instance)
	}
	return r.HandleIntent(ctx, req)
}

func (g *Registry) Resolve(ctx context.Context, instance, ref string) (*pluginv1.RemoteItem, error) {
	r := g.get(instance)
	if r == nil {
		return nil, status.Errorf(codes.Unavailable, "connector instance %q is not running", instance)
	}
	return r.Resolve(ctx, ref)
}
