// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package all

import "testing"

func TestRegistryRegistersProductionProviders(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatalf("Registry(): unexpected error: %v", err)
	}

	wantIDs := []string{"claude-code", "codex", "opencode"}
	providers := reg.All()
	if len(providers) != len(wantIDs) {
		t.Fatalf("Registry().All() returned %d providers, want %d", len(providers), len(wantIDs))
	}
	for i, want := range wantIDs {
		if got := providers[i].Descriptor().ID; got != want {
			t.Errorf("Registry().All()[%d].Descriptor().ID = %q, want %q", i, got, want)
		}
		if _, ok := reg.Get(want); !ok {
			t.Errorf("Registry().Get(%q) = _, false; want true", want)
		}
	}
}
