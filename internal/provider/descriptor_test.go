// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package provider

import "testing"

func TestDescriptorValidate(t *testing.T) {
	valid := func() Descriptor {
		return Descriptor{
			ID: "claude-code",
			Roots: []RootSpec{
				{Base: BaseHome, Rel: ".claude/projects", Glob: "*/*.jsonl"},
			},
		}
	}

	tests := []struct {
		name    string
		d       Descriptor
		wantErr bool
	}{
		{"valid", valid(), false},
		{"empty ID", func() Descriptor { d := valid(); d.ID = ""; return d }(), true},
		{"uppercase ID", func() Descriptor { d := valid(); d.ID = "ClaudeCode"; return d }(), true},
		{"ID with underscore", func() Descriptor { d := valid(); d.ID = "claude_code"; return d }(), true},
		{"no roots", func() Descriptor { d := valid(); d.Roots = nil; return d }(), true},
		{"empty glob", func() Descriptor { d := valid(); d.Roots[0].Glob = ""; return d }(), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.d.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
