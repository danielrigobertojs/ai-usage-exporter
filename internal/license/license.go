// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

// Package license holds the project's attribution facts so they stay
// consultable from code instead of only living in NOTICE and LICENSE.
package license

import "fmt"

const (
	// Project is the canonical project name.
	Project = "ai-usage-exporter"
	// Copyright is the copyright holder notice, matching NOTICE.
	Copyright = "Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval"
	// License is the SPDX identifier under which the project is distributed.
	License = "Apache-2.0"
	// URL is the canonical repository location.
	URL = "https://github.com/danielrigobertojs/ai-usage-exporter"
)

// Attribution returns a single-line attribution block suitable for printing
// in `version` output and for use as an HTTP User-Agent component.
func Attribution() string {
	return fmt.Sprintf("%s — %s — %s — %s", Project, Copyright, License, URL)
}
