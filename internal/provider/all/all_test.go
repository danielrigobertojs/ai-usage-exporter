// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package all

import "testing"

func TestRegistryRegistersClaudeCode(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatalf("Registry(): unexpected error: %v", err)
	}
	if _, ok := reg.Get("claude-code"); !ok {
		t.Error(`Registry().Get("claude-code") = _, false; want true`)
	}
}
