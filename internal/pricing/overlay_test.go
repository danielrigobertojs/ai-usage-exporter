// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package pricing

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOverlayWinsAndFallsThrough(t *testing.T) {
	srv, _ := countingServer(t, sampleModelsDevPayload, http.StatusOK)
	withModelsDevURL(t, srv.URL)

	cfg := baseConfig(t)
	cfg.HTTPClient = srv.Client()
	cfg.OverlayPath = filepath.Join(t.TempDir(), "pricing.json")

	overlayJSON := `{"version":1,"models":{"claude-opus-5":{"input":1,"output":2}}}`
	if err := os.WriteFile(cfg.OverlayPath, []byte(overlayJSON), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cat, err := Load(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cat.Source() != "overlay" {
		t.Fatalf("Source() = %q, want %q", cat.Source(), "overlay")
	}

	r, ok := cat.Lookup("claude-code", "claude-opus-5")
	if !ok {
		t.Fatal("expected claude-opus-5 to resolve")
	}
	if want := 1.0 / 1_000_000; r.Input != want {
		t.Errorf("overlay should win: Input = %v, want %v (models.dev's own value is different)", r.Input, want)
	}

	// gpt-5 is not in the overlay; it must still resolve via the lower
	// (models.dev) tier even though Source() reports "overlay" overall.
	if _, ok := cat.Lookup("codex", "gpt-5"); !ok {
		t.Error("expected gpt-5 to fall through to the models.dev tier")
	}
}

func TestLoadOverlayAbsentIsNotAnError(t *testing.T) {
	srv, _ := countingServer(t, sampleModelsDevPayload, http.StatusOK)
	withModelsDevURL(t, srv.URL)

	cfg := baseConfig(t) // OverlayPath already points at a nonexistent file
	cfg.HTTPClient = srv.Client()

	cat, err := Load(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cat.Source() != "models.dev" {
		t.Errorf("Source() = %q, want %q", cat.Source(), "models.dev")
	}
}

func TestLoadOverlayCorruptIsIgnored(t *testing.T) {
	cfg := baseConfig(t)
	withModelsDevURL(t, "http://127.0.0.1:0")
	if err := os.WriteFile(cfg.OverlayPath, []byte("{not valid"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cat, err := Load(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Load must never error on a corrupt overlay, got: %v", err)
	}
	if cat.Source() != "embedded" {
		t.Errorf("Source() = %q, want %q", cat.Source(), "embedded")
	}
}

func TestLoadOverlayEmptyPath(t *testing.T) {
	if got := loadOverlay(Config{}); got != nil {
		t.Errorf("loadOverlay with no OverlayPath = %v, want nil", got)
	}
}

func TestLoadOverlayUnmentionedModelsUseLowerTiers(t *testing.T) {
	cfg := baseConfig(t)
	withModelsDevURL(t, "http://127.0.0.1:0") // no remote tier available

	overlayJSON := `{"version":1,"models":{"claude-opus-5":{"input":1,"output":2}}}`
	if err := os.WriteFile(cfg.OverlayPath, []byte(overlayJSON), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cat, err := Load(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cat.Source() != "overlay" {
		t.Fatalf("Source() = %q, want %q", cat.Source(), "overlay")
	}

	// Not mentioned by the overlay, but present in the embedded table.
	if _, ok := cat.Lookup("codex", "gpt-5"); !ok {
		t.Error("expected gpt-5 to resolve via the embedded tier")
	}
}
