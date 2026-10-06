// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package pricing

import "os"

// loadOverlay reads and decodes the user's pricing.json, if one exists at
// cfg.OverlayPath. A missing file is the common case (no override
// configured) and is not a degradation: it returns nil silently. An
// existing-but-corrupt overlay also degrades to nil rather than erroring -
// Load's "never fail" invariant applies to every tier, including the one
// a human hand-edited.
func loadOverlay(cfg Config) table {
	if cfg.OverlayPath == "" {
		return nil
	}

	data, err := os.ReadFile(cfg.OverlayPath)
	if err != nil {
		return nil
	}

	t, err := decodeCatalogFile(data)
	if err != nil {
		return nil
	}
	return t
}
