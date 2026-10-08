// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package pricing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/license"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/version"
)

// modelsDevURL is the single remote endpoint this package ever talks to.
// It is a var, not a const, purely so tests can point it at an
// httptest.Server instead of the real host - production code never
// reassigns it.
var modelsDevURL = "https://models.dev/api.json"

// modelsDevProviderPrecedence favors the first-party providers whose public
// list prices best represent the direct billing paths used by this exporter.
// This is a provenance heuristic, not a trust list: bare model IDs still lack
// the provider that produced the usage event. ADR-006 documents the trade-off
// and the condition for replacing this with provider-qualified lookups.
var modelsDevProviderPrecedence = []string{
	"anthropic",
	"openai",
	"opencode",
	"google",
	"xai",
	"moonshotai",
	"deepseek",
	"zhipuai",
	"z-ai",
	"mistral",
	"meta",
	"alibaba",
	"qwen",
}

// modelsDevProvider is one entry of the top-level, provider-id-keyed
// object models.dev returns. Fields this package doesn't need (env, npm,
// doc, ...) are left to json.Unmarshal's default of silently ignoring
// them.
type modelsDevProvider struct {
	Models map[string]modelsDevModel `json:"models"`
}

// modelsDevModel is one model entry. Cost is a pointer so a model with no
// pricing information (an embedding or image model, or one still in
// preview) is distinguishable from one priced at exactly zero.
type modelsDevModel struct {
	Cost *modelRatesUSDPerMillion `json:"cost"`
}

// parseModelsDev decodes a models.dev api.json payload into a table keyed
// by each model's bare ID - the map key within its provider's "models"
// object, not the provider-qualified ID some reseller providers (poe,
// openrouter, ...) list their re-exported models under. Models with no
// cost object are skipped rather than recorded at a false $0: an unpriced
// model must stay "unknown" to CostUSD, never "free".
//
// A model ID that recurs across providers keeps the value from the first
// available provider in modelsDevProviderPrecedence. Providers outside that
// list retain the former alphabetical fallback. The fixed order is necessary
// because providers is decoded into a Go map, whose iteration order is not
// stable.
func parseModelsDev(data []byte) (table, error) {
	var providers map[string]modelsDevProvider
	if err := json.Unmarshal(data, &providers); err != nil {
		return nil, fmt.Errorf("pricing: decode models.dev payload: %w", err)
	}

	t := make(table)
	for _, id := range orderedModelsDevProviderIDs(providers) {
		for modelID, m := range providers[id].Models {
			if m.Cost == nil {
				continue
			}
			if _, exists := t[modelID]; exists {
				continue
			}
			t[modelID] = m.Cost.toRates()
		}
	}
	return t, nil
}

// orderedModelsDevProviderIDs puts known first-party providers first and
// appends all remaining providers alphabetically. It never depends on a map
// traversal order, including when a precedence entry is absent from a payload.
func orderedModelsDevProviderIDs(providers map[string]modelsDevProvider) []string {
	ids := make([]string, 0, len(providers))
	seen := make(map[string]struct{}, len(modelsDevProviderPrecedence))
	for _, id := range modelsDevProviderPrecedence {
		if _, ok := providers[id]; ok {
			ids = append(ids, id)
			seen[id] = struct{}{}
		}
	}

	remaining := make([]string, 0, len(providers)-len(ids))
	for id := range providers {
		if _, ok := seen[id]; !ok {
			remaining = append(remaining, id)
		}
	}
	sort.Strings(remaining)
	return append(ids, remaining...)
}

// userAgent identifies this binary to models.dev, so whoever operates it
// can see who is using the dataset it donates for free - see NOTICE for
// the attribution this is a courtesy counterpart to.
func userAgent() string {
	return fmt.Sprintf("ai-usage-exporter/%s (+%s)", version.Version, license.URL)
}

// fetchModelsDevRaw performs the single HTTP call this package ever makes,
// returning the response body unparsed so the caller can both validate it
// (parseModelsDev) and persist the exact bytes served, keeping the on-disk
// cache byte-identical to what models.dev sent.
func fetchModelsDevRaw(ctx context.Context, cfg Config) ([]byte, error) {
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: cfg.HTTPTimeout}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsDevURL, nil)
	if err != nil {
		return nil, fmt.Errorf("pricing: build models.dev request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent())

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pricing: fetch models.dev: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pricing: models.dev returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("pricing: read models.dev response: %w", err)
	}
	return body, nil
}
