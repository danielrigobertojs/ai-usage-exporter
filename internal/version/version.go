// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

// Package version holds build-time metadata injected via -ldflags.
package version

import (
	"fmt"
	"runtime"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/license"
)

var (
	// Version is the released version, e.g. "v0.1.0". Set to "dev" for local builds.
	Version = "dev"
	// Commit is the git commit SHA the binary was built from.
	Commit = "none"
	// BuildDate is the UTC build timestamp in RFC3339 format.
	BuildDate = "unknown"
)

// String returns a single-line, human-readable build identifier followed by
// the project's attribution line, so license and copyright travel with the
// binary wherever this string is printed.
func String() string {
	return fmt.Sprintf("ai-usage-exporter %s (%s, built %s, %s)\n%s",
		Version, Commit, BuildDate, runtime.Version(), license.Attribution())
}
