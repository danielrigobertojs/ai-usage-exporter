// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package provider

import (
	"context"
	"testing"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
)

// stubProvider is a minimal Provider used only to exercise Registry: it
// does not parse anything, it just carries an ID.
type stubProvider struct{ id string }

func (s stubProvider) Descriptor() Descriptor {
	return Descriptor{ID: s.id, Roots: []RootSpec{{Base: BaseHome, Rel: ".x", Glob: "*.jsonl"}}}
}

func (s stubProvider) Parse(context.Context, Source, func(model.UsageEvent) error) error {
	return nil
}

func TestRegistryRegisterDuplicate(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(stubProvider{id: "codex"}); err != nil {
		t.Fatalf("first Register: unexpected error: %v", err)
	}
	if err := r.Register(stubProvider{id: "codex"}); err == nil {
		t.Fatal("second Register with duplicate ID: want error, got nil")
	}
}

func TestRegistryAllPreservesOrder(t *testing.T) {
	r := NewRegistry()
	ids := []string{"a", "b", "c", "d", "e"}
	for _, id := range ids {
		if err := r.Register(stubProvider{id: id}); err != nil {
			t.Fatalf("Register(%q): unexpected error: %v", id, err)
		}
	}

	all := r.All()
	if len(all) != len(ids) {
		t.Fatalf("All(): got %d providers, want %d", len(all), len(ids))
	}
	for i, p := range all {
		if got := p.Descriptor().ID; got != ids[i] {
			t.Errorf("All()[%d].Descriptor().ID = %q, want %q", i, got, ids[i])
		}
	}
}

func TestRegistryGet(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(stubProvider{id: "claude-code"}); err != nil {
		t.Fatalf("Register: unexpected error: %v", err)
	}

	if p, ok := r.Get("claude-code"); !ok || p.Descriptor().ID != "claude-code" {
		t.Fatalf("Get(%q) = %v, %v; want a provider with that ID and true", "claude-code", p, ok)
	}
	if _, ok := r.Get("does-not-exist"); ok {
		t.Fatal("Get(\"does-not-exist\") = _, true; want false")
	}
}
