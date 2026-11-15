package gateway

import (
	"fmt"
	"sort"
	"sync"
)

// Factory builds a Gateway for one merchant account.
type Factory func(cfg Config) (Gateway, error)

// Registry maps gateway names to their adapter factories. Adding a gateway means registering one more factory.
type Registry struct {
	mu        sync.RWMutex
	factories map[Name]Factory
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{factories: make(map[Name]Factory)}
}

// Register adds a factory. It panics on a duplicate name: that is a wiring bug, not a runtime condition.
func (r *Registry) Register(name Name, factory Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.factories[name]; exists {
		panic(fmt.Sprintf("gateway: %q registered twice", name))
	}
	r.factories[name] = factory
}

// Has reports whether a gateway name is registered.
func (r *Registry) Has(name Name) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.factories[name]
	return ok
}

// New builds the Gateway for cfg, or returns ErrUnknownGateway.
func (r *Registry) New(cfg Config) (Gateway, error) {
	r.mu.RLock()
	factory, ok := r.factories[cfg.Gateway]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownGateway, cfg.Gateway)
	}
	return factory(cfg)
}

// Names lists the registered gateway names in alphabetical order.
func (r *Registry) Names() []Name {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]Name, 0, len(r.factories))
	for n := range r.factories {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })
	return names
}
