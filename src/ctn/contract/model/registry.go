// Package registry provides a thread-safe provider registry for model
// inference backends. Consumers register providers by name and resolve them
// when running inference. The registry itself knows nothing about specific
// provider implementations; those are registered by adapters or the host.
package model

import (
	"context"
	"fmt"
	"sync"
)

// Registry maps provider names to Provider implementations. It is safe for
// concurrent use.
type Registry struct {
	mu         sync.RWMutex
	providers  map[string]Provider
}

// NewRegistry creates an empty provider registry.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

// Register adds a provider to the registry under its name. Registering a
// provider with the same name replaces the previous one.
func (r *Registry) Register(name string, p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[name] = p
}

// Unregister removes a provider from the registry.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.providers, name)
}

// Resolve returns the provider registered under the given name, or
// ErrUnavailable if no such provider is registered.
func (r *Registry) Resolve(name string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("%w: no provider registered for %q", ErrUnavailable, name)
	}
	return p, nil
}

// Providers returns the names of all registered providers.
func (r *Registry) Providers() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}

// RunModel resolves a model from the store, selects a provider from the
// registry, loads a session, runs inference, and closes the session. It is
// the portable helper that product commands (such as ctn model run) build on.
// ctx is used for cancellation and timeouts.
func RunModel(ctx context.Context, reg *Registry, store *Store, ref Reference, inputs []Tensor) ([]Tensor, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	model, err := store.Resolve(ctx, ref.Provider, ref.Name, ref.Version)
	if err != nil {
		return nil, err
	}
	provider, err := reg.Resolve(ref.Provider)
	if err != nil {
		return nil, err
	}
	session, err := provider.Load(ctx, model)
	if err != nil {
		return nil, err
	}
	defer func() { _ = session.Close() }()
	return session.Infer(ctx, inputs)
}
