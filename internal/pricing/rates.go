// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

// Package pricing resolves USD-per-token rates for a (tool, model) pair and
// turns a model.UsageEvent's token counts into an estimated cost. It never
// returns an error for a bad network, a stale or corrupt cache, or an
// unparseable remote payload - Load degrades through three layers (user
// overlay, models.dev cache, embedded table) and the embedded table, built
// from go:embed, is the floor that guarantees /metrics always has a price
// to serve, or an explicit "unknown" instead of a silent zero.
package pricing

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
)

// Rates are USD per ONE token, never per million - callers multiply
// directly against a raw token count. Catalog implementations build these
// by dividing the USD-per-million figures in a rates file by 1e6.
type Rates struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
	Reasoning  float64
}

// Catalog resolves pricing for a (tool, model) pair.
type Catalog interface {
	// Lookup tries the bare modelID first and only falls back to the
	// "tool:modelID" qualified key when the bare one is absent, matching
	// CodexBar's resolution order. It reports false for an unknown model -
	// never a zero Rates, which would silently claim the model is free.
	Lookup(tool, modelID string) (Rates, bool)
	// Source identifies which tier produced the catalog's current pricing:
	// "overlay" | "models.dev" | "embedded".
	Source() string
}

// CostUSD applies c's rates to e's token counts, summed independently per
// token class. It returns (0, false), never a silent zero cost, when e's
// model is unknown to c - the collector must expose the tokens and omit
// ai_usage_cost_usd for that series rather than publish a $0 estimate.
func CostUSD(c Catalog, e model.UsageEvent) (float64, bool) {
	rates, ok := c.Lookup(e.Tool, e.Model)
	if !ok {
		return 0, false
	}

	var total float64
	for class, count := range e.Tokens {
		var rate float64
		switch class {
		case model.TokenInput:
			rate = rates.Input
		case model.TokenOutput:
			rate = rates.Output
		case model.TokenCacheRead:
			rate = rates.CacheRead
		case model.TokenCacheWrite:
			rate = rates.CacheWrite
		case model.TokenReasoning:
			rate = rates.Reasoning
		default:
			continue
		}
		total += float64(count) * rate
	}
	return total, true
}

// modelRatesUSDPerMillion is the on-disk shape of one model's entry in a
// rates file (embedded.json, the models.dev cache, or a user overlay):
// USD per million tokens, matching how models.dev itself publishes prices.
type modelRatesUSDPerMillion struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
	Reasoning  float64 `json:"reasoning"`
}

func (m modelRatesUSDPerMillion) toRates() Rates {
	const perMillion = 1_000_000
	return Rates{
		Input:      m.Input / perMillion,
		Output:     m.Output / perMillion,
		CacheRead:  m.CacheRead / perMillion,
		CacheWrite: m.CacheWrite / perMillion,
		Reasoning:  m.Reasoning / perMillion,
	}
}

// catalogFile is the shared shape of embedded.json and a user's pricing.json
// overlay, so a user can copy the embedded table and edit it in place.
type catalogFile struct {
	Version int                                `json:"version"`
	Models  map[string]modelRatesUSDPerMillion `json:"models"`
}

// decodeCatalogFile parses data as a catalogFile and converts it to a
// table. It is the only place embedded.json and an overlay file's bytes
// turn into Rates.
func decodeCatalogFile(data []byte) (table, error) {
	var f catalogFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("pricing: decode rates file: %w", err)
	}
	t := make(table, len(f.Models))
	for id, raw := range f.Models {
		t[id] = raw.toRates()
	}
	return t, nil
}

// table is one tier's model-id -> Rates mapping.
type table map[string]Rates

// lookup applies the bare-key-first, qualified-key-fallback resolution
// order: a model whose reported ID collides across tools would otherwise
// need every tool to agree on a prefix, which none of them do.
func (t table) lookup(tool, modelID string) (Rates, bool) {
	if t == nil {
		return Rates{}, false
	}
	if r, ok := t[modelID]; ok {
		return r, true
	}
	if tool != "" {
		if r, ok := t[tool+":"+modelID]; ok {
			return r, true
		}
	}
	return Rates{}, false
}

// layeredCatalog resolves a model against overlay, then remote (models.dev,
// live or cached), then embedded, in that order. source names the highest
// tier that has any data loaded, independent of which tier actually
// answered a given Lookup - overriding one model via overlay still reports
// "overlay" even though every other model falls through to lower tiers.
type layeredCatalog struct {
	overlay  table
	remote   table
	embedded table
	source   string
}

func (c *layeredCatalog) Lookup(tool, modelID string) (Rates, bool) {
	if r, ok := c.overlay.lookup(tool, modelID); ok {
		return r, true
	}
	if r, ok := c.remote.lookup(tool, modelID); ok {
		return r, true
	}
	if r, ok := c.embedded.lookup(tool, modelID); ok {
		return r, true
	}
	return Rates{}, false
}

func (c *layeredCatalog) Source() string {
	return c.source
}

// Config controls how Load resolves pricing. Every field has a default
// applied by withDefaults, so a zero Config is valid.
type Config struct {
	CacheDir    string        // default: os.UserCacheDir()/ai-usage-exporter
	OverlayPath string        // default: <xdg_config>/ai-usage-exporter/pricing.json
	TTL         time.Duration // default 24h
	Offline     bool          // true: Load never touches the network
	HTTPTimeout time.Duration // default 5s
	HTTPClient  *http.Client  // nil uses a client built from HTTPTimeout; tests inject their own
}

const defaultTTL = 24 * time.Hour
const defaultHTTPTimeout = 5 * time.Second

// withDefaults fills every unset field, resolving OS-specific directories
// through os.UserCacheDir/os.UserConfigDir rather than a hand-built path -
// required on Windows, which has no XDG directories to concatenate "~"
// onto. A directory that fails to resolve is left empty, which disables
// that layer's disk I/O rather than operating on a bogus relative path.
func (c Config) withDefaults() Config {
	if c.CacheDir == "" {
		if dir, err := os.UserCacheDir(); err == nil {
			c.CacheDir = filepath.Join(dir, "ai-usage-exporter")
		}
	}
	if c.OverlayPath == "" {
		if dir, err := os.UserConfigDir(); err == nil {
			c.OverlayPath = filepath.Join(dir, "ai-usage-exporter", "pricing.json")
		}
	}
	if c.TTL == 0 {
		c.TTL = defaultTTL
	}
	if c.HTTPTimeout == 0 {
		c.HTTPTimeout = defaultHTTPTimeout
	}
	return c
}
