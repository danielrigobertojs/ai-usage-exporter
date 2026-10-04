// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/version"
)

func main() {
	fmt.Println(version.String())
}
