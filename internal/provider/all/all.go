// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

// Package all is the single place that knows about every production
// provider. internal/provider stays free of a dependency on any of its own
// concrete providers - all imports it, never the other way around - so
// cmd/ gets one import for "every real provider" instead of one per tool,
// and adding a provider never risks an import cycle.
package all

import (
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider/claudecode"
)

// Registry returns every production provider, registered in a stable
// order. The fake provider under internal/provider/fake is deliberately
// absent: it is a test fixture, not a shipped tool.
func Registry() (*provider.Registry, error) {
	reg := provider.NewRegistry()
	providers := []provider.Provider{
		claudecode.New(),
	}
	for _, p := range providers {
		if err := reg.Register(p); err != nil {
			return nil, err
		}
	}
	return reg, nil
}
