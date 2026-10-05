// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package main

import (
	"fmt"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/version"
)

func main() {
	fmt.Println(version.String())
}
