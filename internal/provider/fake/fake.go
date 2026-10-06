// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

// Package fake implements a minimal provider.Provider used only by tests:
// it declares a valid Descriptor and emits deterministic synthetic events
// instead of parsing a real tool's log format. See docs/adding-a-provider.md,
// which walks through this file step by step as the worked example.
package fake

import (
	"context"
	"fmt"
	"time"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider"
)

// New returns a Provider that emits n events per Source it is asked to
// parse, always the same n events for the same Source - deterministic, so
// registry and discover tests can assert on exact output.
func New(n int) provider.Provider {
	return fakeProvider{eventsPerSource: n}
}

type fakeProvider struct {
	eventsPerSource int
}

func (fakeProvider) Descriptor() provider.Descriptor {
	return provider.Descriptor{
		ID:          "fake",
		DisplayName: "Fake Provider",
		Kind:        provider.SourceJSONL,
		Roots: []provider.RootSpec{
			{Base: provider.BaseHome, Rel: ".fake", Glob: "*.jsonl"},
		},
		Capabilities: provider.Capabilities{HasTokens: true, HasToolCalls: true},
	}
}

func (p fakeProvider) Parse(ctx context.Context, src provider.Source, emit func(model.UsageEvent) error) error {
	for i := 0; i < p.eventsPerSource; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		evt := model.UsageEvent{
			Key: model.EventKey{
				Tool:      "fake",
				SessionID: src.Path,
				MessageID: fmt.Sprintf("%s#%d", src.Path, i),
			},
			Tool:      "fake",
			Model:     "fake-model",
			Timestamp: src.ModTime.Add(time.Duration(i) * time.Second).UTC(),
			Tokens: map[model.TokenClass]int64{
				model.TokenInput:  int64(10 + i),
				model.TokenOutput: int64(5 + i),
			},
			ToolCalls: 1,
		}
		if err := emit(evt); err != nil {
			return err
		}
	}
	return nil
}
