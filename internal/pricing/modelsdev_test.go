// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package pricing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
