// SPDX-License-Identifier: Apache-2.0

// Package version holds build-time metadata injected via -ldflags.
package version

import (
	"fmt"
	"runtime"
)

var (
	// Version is the released version, e.g. "v0.1.0". Set to "dev" for local builds.
	Version = "dev"
	// Commit is the git commit SHA the binary was built from.
	Commit = "none"
	// BuildDate is the UTC build timestamp in RFC3339 format.
	BuildDate = "unknown"
)

// String returns a single-line, human-readable build identifier.
func String() string {
	return fmt.Sprintf("ai-usage-exporter %s (%s, built %s, %s)", Version, Commit, BuildDate, runtime.Version())
}
