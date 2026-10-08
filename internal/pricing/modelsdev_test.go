// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package pricing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readModelsDevFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %q: %v", name, err)
	}
	return data
}

const sampleModelsDevPayload = `{
  "anthropic": {
    "id": "anthropic",
    "models": {
      "claude-opus-5": {"id": "claude-opus-5", "cost": {"input": 5, "output": 25, "cache_read": 0.5, "cache_write": 6.25}}
    }
  },
  "openai": {
    "id": "openai",
    "models": {
      "gpt-5": {"id": "gpt-5", "cost": {"input": 1.25, "output": 10, "cache_read": 0.125}},
      "gpt-image-1": {"id": "gpt-image-1"}
    }
  },
  "poe": {
    "id": "poe",
    "models": {
      "anthropic/claude-opus-5": {"id": "anthropic/claude-opus-5", "cost": {"input": 999, "output": 999}},
      "gpt-5": {"id": "gpt-5", "cost": {"input": 999, "output": 999}}
    }
  }
}`

func TestParseModelsDev(t *testing.T) {
	got, err := parseModelsDev([]byte(sampleModelsDevPayload))
	if err != nil {
		t.Fatalf("parseModelsDev: %v", err)
	}

	if r, ok := got["claude-opus-5"]; !ok || r.Input != 5.0/1_000_000 {
		t.Errorf("claude-opus-5 = %+v, ok=%v", r, ok)
	}

	// gpt-5 recurs in both "openai" (canonical) and "poe" (reseller); the
	// first value seen must win, so a reseller can never clobber the
	// canonical provider's price via map iteration order.
	if r, ok := got["gpt-5"]; !ok || r.Input != 1.25/1_000_000 {
		t.Errorf("gpt-5 should keep its first-seen price, got %+v, ok=%v", r, ok)
	}

	if _, ok := got["gpt-image-1"]; ok {
		t.Error("a model with no cost object must stay absent, never priced at a false 0")
	}

	if _, ok := got["anthropic/claude-opus-5"]; !ok {
		t.Error("a reseller-qualified key with no bare collision should still be recorded")
	}
}

func TestParseModelsDevInvalidJSON(t *testing.T) {
	if _, err := parseModelsDev([]byte("not json")); err == nil {
		t.Fatal("expected an error for invalid JSON, got nil")
	}
}

func TestParseModelsDevProviderPrecedence(t *testing.T) {
	fixture := readModelsDevFixture(t, "provider-precedence.json")

	for _, tt := range []struct {
		name      string
		modelID   string
		wantInput float64
		wantCache float64
		wantFound bool
	}{
		{
			name:      "first-party cache pricing wins over alphabetically earlier reseller",
			modelID:   "claude-opus-5",
			wantInput: 5.0 / 1_000_000,
			wantCache: 0.5 / 1_000_000,
			wantFound: true,
		},
		{
			name:      "reseller-only model keeps alphabetical fallback",
			modelID:   "reseller-only",
			wantInput: 2.0 / 1_000_000,
			wantFound: true,
		},
		{
			name:      "null cost remains unpriced",
			modelID:   "unpriced-model",
			wantFound: false,
		},
		{
			name:      "explicit all-zero cost remains priced",
			modelID:   "zero-priced-model",
			wantFound: true,
		},
		{
			name:      "precedence order wins when both first-party providers publish the ID",
			modelID:   "shared-first-party-id",
			wantInput: 3.0 / 1_000_000,
			wantFound: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseModelsDev(fixture)
			if err != nil {
				t.Fatalf("parseModelsDev: %v", err)
			}
			rates, ok := got[tt.modelID]
			if ok != tt.wantFound {
				t.Fatalf("model %q found=%v, want %v", tt.modelID, ok, tt.wantFound)
			}
			if !tt.wantFound {
				return
			}
			if rates.Input != tt.wantInput || rates.CacheRead != tt.wantCache {
				t.Errorf("model %q rates=%+v, want input=%v cache_read=%v", tt.modelID, rates, tt.wantInput, tt.wantCache)
			}
		})
	}
}

func TestParseModelsDevDeterministic(t *testing.T) {
	fixture := readModelsDevFixture(t, "provider-precedence.json")
	want, err := parseModelsDev(fixture)
	if err != nil {
		t.Fatalf("parseModelsDev first run: %v", err)
	}
	for i := 0; i < 100; i++ {
		got, err := parseModelsDev(fixture)
		if err != nil {
			t.Fatalf("parseModelsDev run %d: %v", i+2, err)
		}
		if !tablesEqual(got, want) {
			t.Fatalf("parseModelsDev run %d = %#v, want %#v", i+2, got, want)
		}
	}
}

func tablesEqual(got, want table) bool {
	if len(got) != len(want) {
		return false
	}
	for id, wantRates := range want {
		if gotRates, ok := got[id]; !ok || gotRates != wantRates {
			return false
		}
	}
	return true
}

// withModelsDevURL points the package-level modelsDevURL at url for the
// duration of the test, restoring it on cleanup - the only seam this
// package needs to keep its single real endpoint out of unit tests.
func withModelsDevURL(t *testing.T, url string) {
	t.Helper()
	original := modelsDevURL
	modelsDevURL = url
	t.Cleanup(func() { modelsDevURL = original })
}

func TestFetchModelsDevRaw(t *testing.T) {
	t.Run("success sends an identifying User-Agent", func(t *testing.T) {
		var gotUA string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotUA = r.Header.Get("User-Agent")
			w.Write([]byte(sampleModelsDevPayload))
		}))
		defer srv.Close()
		withModelsDevURL(t, srv.URL)

		cfg := Config{HTTPClient: srv.Client()}
		body, err := fetchModelsDevRaw(context.Background(), cfg)
		if err != nil {
			t.Fatalf("fetchModelsDevRaw: %v", err)
		}
		if string(body) != sampleModelsDevPayload {
			t.Errorf("body = %q, want the sample payload", body)
		}
		if !strings.HasPrefix(gotUA, "ai-usage-exporter/") {
			t.Errorf("User-Agent = %q, want it to start with %q", gotUA, "ai-usage-exporter/")
		}
	})

	t.Run("non-200 status is an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()
		withModelsDevURL(t, srv.URL)

		cfg := Config{HTTPClient: srv.Client()}
		if _, err := fetchModelsDevRaw(context.Background(), cfg); err == nil {
			t.Fatal("expected an error for a 500 response")
		}
	})
}
