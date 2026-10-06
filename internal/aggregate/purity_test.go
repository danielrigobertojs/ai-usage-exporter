// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package aggregate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPurityNoForbiddenDeps guards the domain-core invariant: internal/model
// and internal/aggregate must stay free of disk, network, and database I/O
// so the providers (tickets 4-8) can program against pure, parameter-driven
// types without stubbing any of that out in tests.
func TestPurityNoForbiddenDeps(t *testing.T) {
	forbidden := map[string]bool{"os": true, "net": true, "database/sql": true}

	for _, pkg := range []string{
		"github.com/danielrigobertojs/ai-usage-exporter/internal/model",
		"github.com/danielrigobertojs/ai-usage-exporter/internal/aggregate",
	} {
		out, err := exec.Command("go", "list", "-deps", pkg).Output()
		if err != nil {
			t.Fatalf("go list -deps %s: %v", pkg, err)
		}
		for _, dep := range strings.Fields(string(out)) {
			if forbidden[dep] {
				t.Errorf("%s transitively imports forbidden package %q", pkg, dep)
			}
		}
	}
}

// TestPurityNoTimeNowLiteral guards the "now enters via New" invariant: the
// scan instant must be injected by the caller, never read from the global
// clock inside the pure core, or two scans of the same log history would
// stop being reproducible.
func TestPurityNoTimeNowLiteral(t *testing.T) {
	for _, dir := range []string{filepath.Join("..", "model"), "."} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read dir %s: %v", dir, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(dir, name)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			if strings.Contains(string(data), "time.Now(") {
				t.Errorf("%s calls time.Now(); the scan instant must be injected via New", path)
			}
		}
	}
}
