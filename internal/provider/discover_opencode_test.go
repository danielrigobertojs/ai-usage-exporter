// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package provider_test

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider/opencode"
)

func TestDiscoverOpenCodeDeduplicatesOverlappingRoots(t *testing.T) {
	tests := []struct {
		name            string
		goos            string
		env             map[string]string
		path            string
		caseInsensitive bool
	}{
		{
			name: "windows OPENCODE_DATA_HOME applies to both roots",
			goos: "windows",
			env:  map[string]string{"OPENCODE_DATA_HOME": "/virtual/data"},
			path: "virtual/data/opencode/opencode.db",
		},
		{
			name: "windows XDG_DATA_HOME and LOCALAPPDATA overlap case insensitively",
			goos: "windows",
			env: map[string]string{
				"XDG_DATA_HOME": "/Virtual/Data",
				"LOCALAPPDATA":  "/virtual/data",
			},
			path:            "virtual/data/opencode/opencode.db",
			caseInsensitive: true,
		},
		{
			name: "windows XDG_DATA_HOME and LOCALAPPDATA overlap",
			goos: "windows",
			env: map[string]string{
				"XDG_DATA_HOME": "/virtual/data",
				"LOCALAPPDATA":  "/virtual/data",
			},
			path: "virtual/data/opencode/opencode.db",
		},
		{
			name: "darwin OPENCODE_DATA_HOME has one root",
			goos: "darwin",
			env:  map[string]string{"OPENCODE_DATA_HOME": "/virtual/data"},
			path: "virtual/data/opencode/opencode.db",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filesystem := fs.FS(fstest.MapFS{
				tt.path: {Data: []byte("SQLite format 3"), ModTime: time.Now()},
			})
			if tt.caseInsensitive {
				filesystem = caseInsensitiveFS{inner: filesystem}
			}
			env := provider.Env{
				GOOS: tt.goos,
				Home: "home",
				Getenv: func(name string) string {
					return tt.env[name]
				},
				FS: filesystem,
			}

			sources, stats, err := provider.Discover(
				context.Background(), opencode.New().Descriptor(), env, provider.DefaultBudget(time.Now()),
			)
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}
			if got := len(sources); got != 1 {
				t.Fatalf("len(sources) = %d, want 1", got)
			}
			if got := len(stats.ResolvedRoots); got != 1 {
				t.Errorf("len(ResolvedRoots) = %d, want 1", got)
			}
		})
	}
}

// caseInsensitiveFS emulates the ordinary NTFS name lookup behavior while
// keeping the test deterministic on non-Windows hosts.
type caseInsensitiveFS struct {
	inner fs.FS
}

func (f caseInsensitiveFS) Open(name string) (fs.File, error) {
	return f.inner.Open(strings.ToLower(name))
}
