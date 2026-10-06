// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package pricing

import (
	"testing"
	"time"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
)

func TestDecodeCatalogFilePerMillionConversion(t *testing.T) {
	data := []byte(`{"version":1,"models":{"m":{"input":15.0}}}`)

	got, err := decodeCatalogFile(data)
	if err != nil {
		t.Fatalf("decodeCatalogFile: %v", err)
	}

	want := 15.0 / 1_000_000
	if got["m"].Input != want {
		t.Errorf("Input = %v, want %v", got["m"].Input, want)
	}
	if got["m"].Output != 0 || got["m"].CacheRead != 0 || got["m"].CacheWrite != 0 || got["m"].Reasoning != 0 {
		t.Errorf("unset fields should decode to zero, got %+v", got["m"])
	}
}

func TestDecodeCatalogFileInvalidJSON(t *testing.T) {
	if _, err := decodeCatalogFile([]byte("not json")); err == nil {
		t.Fatal("expected an error for invalid JSON, got nil")
	}
}

func TestTableLookup(t *testing.T) {
	bare := Rates{Input: 1}
	qualified := Rates{Input: 2}

	tests := []struct {
		name    string
		t       table
		tool    string
		modelID string
		want    Rates
		wantOK  bool
	}{
		{
			name:    "bare key wins over qualified",
			t:       table{"gpt-5-codex": bare, "codex:gpt-5-codex": qualified},
			tool:    "codex",
			modelID: "gpt-5-codex",
			want:    bare,
			wantOK:  true,
		},
		{
			name:    "falls back to qualified when bare is absent",
			t:       table{"codex:gpt-5-codex": qualified},
			tool:    "codex",
			modelID: "gpt-5-codex",
			want:    qualified,
			wantOK:  true,
		},
		{
			name:    "unknown model",
			t:       table{"codex:gpt-5-codex": qualified},
			tool:    "codex",
			modelID: "gpt-4",
			want:    Rates{},
			wantOK:  false,
		},
		{
			name:    "nil table",
			t:       nil,
			tool:    "codex",
			modelID: "gpt-5-codex",
			want:    Rates{},
			wantOK:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.t.lookup(tc.tool, tc.modelID)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if got != tc.want {
				t.Errorf("rates = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestLayeredCatalogLookupPrecedence(t *testing.T) {
	c := &layeredCatalog{
		overlay:  table{"m": {Input: 1}},
		remote:   table{"m": {Input: 2}, "n": {Input: 20}},
		embedded: table{"m": {Input: 3}, "n": {Input: 30}, "o": {Input: 300}},
	}

	if r, ok := c.Lookup("tool", "m"); !ok || r.Input != 1 {
		t.Errorf("overlay should win for m: got %+v, ok=%v", r, ok)
	}
	if r, ok := c.Lookup("tool", "n"); !ok || r.Input != 20 {
		t.Errorf("remote should win for n: got %+v, ok=%v", r, ok)
	}
	if r, ok := c.Lookup("tool", "o"); !ok || r.Input != 300 {
		t.Errorf("embedded should answer for o: got %+v, ok=%v", r, ok)
	}
	if _, ok := c.Lookup("tool", "unknown"); ok {
		t.Error("unknown model should report false")
	}
}

func TestCostUSD(t *testing.T) {
	c := &layeredCatalog{embedded: table{
		"known-model": {Input: 0.001, Output: 0.002, CacheRead: 0.0001, CacheWrite: 0.0002, Reasoning: 0.003},
	}}

	t.Run("known model sums every class independently", func(t *testing.T) {
		e := model.UsageEvent{
			Tool:  "tool",
			Model: "known-model",
			Tokens: map[model.TokenClass]int64{
				model.TokenInput:                 1000,
				model.TokenOutput:                500,
				model.TokenCacheRead:             200,
				model.TokenCacheWrite:            100,
				model.TokenReasoning:             50,
				model.TokenClass("unrecognized"): 999,
			},
		}
		got, ok := CostUSD(c, e)
		if !ok {
			t.Fatal("expected ok=true for a known model")
		}
		want := 1000*0.001 + 500*0.002 + 200*0.0001 + 100*0.0002 + 50*0.003
		if got != want {
			t.Errorf("cost = %v, want %v (an unrecognized token class must not contribute)", got, want)
		}
	})

	t.Run("unknown model never returns a silent zero", func(t *testing.T) {
		e := model.UsageEvent{Tool: "tool", Model: "unknown-model", Tokens: map[model.TokenClass]int64{model.TokenInput: 1000}}
		got, ok := CostUSD(c, e)
		if ok {
			t.Fatal("expected ok=false for an unknown model")
		}
		if got != 0 {
			t.Errorf("cost = %v, want 0", got)
		}
	})
}

func TestEmbeddedTablePanicsOnCorruptData(t *testing.T) {
	original := embeddedJSON
	embeddedJSON = []byte("not json")
	defer func() { embeddedJSON = original }()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected embeddedTable to panic on invalid embedded.json")
		}
	}()
	embeddedTable()
}

func TestConfigWithDefaults(t *testing.T) {
	got := Config{}.withDefaults()

	if got.TTL != defaultTTL {
		t.Errorf("TTL = %v, want %v", got.TTL, defaultTTL)
	}
	if got.HTTPTimeout != defaultHTTPTimeout {
		t.Errorf("HTTPTimeout = %v, want %v", got.HTTPTimeout, defaultHTTPTimeout)
	}
	if got.CacheDir == "" {
		t.Error("expected a default CacheDir to be resolved")
	}
	if got.OverlayPath == "" {
		t.Error("expected a default OverlayPath to be resolved")
	}

	explicit := Config{CacheDir: "/custom/cache", OverlayPath: "/custom/overlay.json", TTL: time.Minute, HTTPTimeout: time.Second}.withDefaults()
	if explicit.CacheDir != "/custom/cache" || explicit.OverlayPath != "/custom/overlay.json" || explicit.TTL != time.Minute || explicit.HTTPTimeout != time.Second {
		t.Errorf("withDefaults must not override explicitly set fields, got %+v", explicit)
	}
}

func TestEmbeddedJSONDecodes(t *testing.T) {
	c := Embedded()
	if c.Source() != "embedded" {
		t.Errorf("Source() = %q, want %q", c.Source(), "embedded")
	}
	if _, ok := c.Lookup("claude-code", "claude-opus-5"); !ok {
		t.Error("expected the embedded table to know claude-opus-5")
	}
	if _, ok := c.Lookup("codex", "does-not-exist"); ok {
		t.Error("expected an unknown model to report false")
	}
}
