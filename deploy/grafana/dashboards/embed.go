// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

// Package dashboards embeds the Grafana dashboard JSON that ships in this
// directory, so internal/dashboard can validate it in CI without a second,
// drift-prone copy living under internal/. go:embed cannot reach outside
// its own package directory, which is why the embed lives next to the JSON
// instead of inside internal/dashboard.
package dashboards

import _ "embed"

//go:embed ai-usage-overview.json
var AIUsageOverviewJSON []byte

//go:embed ai-usage-live.json
var AIUsageLiveJSON []byte
