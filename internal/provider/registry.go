// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package provider

import "fmt"

// Registry is an ordered collection of Providers plus an ID index for
// lookup - the same shape CodexBar's own ProviderCatalog uses, which is why
// adding a tool there is cheap: register it once, look it up by ID anywhere
// else, and the registration order stays the iteration order.
type Registry struct {
	order []Provider
	index map[string]int
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{index: make(map[string]int)}
}

// Register adds p, keyed by its Descriptor's ID. It errors if that ID is
// already registered rather than silently overwriting it.
func (r *Registry) Register(p Provider) error {
	id := p.Descriptor().ID
	if _, exists := r.index[id]; exists {
		return fmt.Errorf("provider: %q is already registered", id)
	}
	r.index[id] = len(r.order)
	r.order = append(r.order, p)
	return nil
}

// All returns every registered Provider in registration order.
func (r *Registry) All() []Provider {
	out := make([]Provider, len(r.order))
	copy(out, r.order)
	return out
}

// Get returns the Provider registered under id, if any.
func (r *Registry) Get(id string) (Provider, bool) {
	i, ok := r.index[id]
	if !ok {
		return nil, false
	}
	return r.order[i], true
}
