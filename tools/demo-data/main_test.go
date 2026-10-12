// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunWritesOpenCodeAtProviderPath(t *testing.T) {
	out := t.TempDir()
	if err := run(out); err != nil {
		t.Fatalf("run(%q): %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(out, "opencode", "opencode.db")); err != nil {
		t.Fatalf("OpenCode database not at provider path: %v", err)
	}
}
