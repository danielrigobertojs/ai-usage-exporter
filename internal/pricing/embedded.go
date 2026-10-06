// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package pricing

import _ "embed"

//go:embed embedded.json
var embeddedJSON []byte

// embeddedTable decodes the data go:embed compiled into the binary. A
// decode failure here is a build-time defect (embedded.json is static,
// shipped, and covered by TestEmbeddedJSONDecodes), never a runtime
// condition a deployed binary can hit - so, unlike every other tier in
// this package, it panics instead of degrading.
func embeddedTable() table {
	t, err := decodeCatalogFile(embeddedJSON)
	if err != nil {
		panic("pricing: embedded.json is invalid: " + err.Error())
	}
	return t
}

// Embedded returns a Catalog backed solely by the embedded table, with no
// overlay and no models.dev tier. It is the floor Load always falls back
// to, exposed directly for callers (and tests) that want pricing without
// touching disk or network at all.
func Embedded() Catalog {
	return &layeredCatalog{embedded: embeddedTable(), source: "embedded"}
}
