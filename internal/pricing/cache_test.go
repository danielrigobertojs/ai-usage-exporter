// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package pricing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// baseConfig returns a Config rooted entirely under t.TempDir(), with no
// overlay file, so cache/TTL/degradation tests never touch the real
// filesystem outside their own sandbox.
func baseConfig(t *testing.T) Config {
	t.Helper()
	dir := t.TempDir()
	return Config{
		CacheDir:    filepath.Join(dir, "cache"),
		OverlayPath: filepath.Join(dir, "no-such-overlay.json"),
		TTL:         defaultTTL,
	}
}

func countingServer(t *testing.T, body string, status int) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestLoadWritesCacheOnFirstFetch(t *testing.T) {
	srv, hits := countingServer(t, sampleModelsDevPayload, http.StatusOK)
	withModelsDevURL(t, srv.URL)

	cfg := baseConfig(t)
	cfg.HTTPClient = srv.Client()

	cat, err := Load(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cat.Source() != "models.dev" {
		t.Errorf("Source() = %q, want %q", cat.Source(), "models.dev")
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Errorf("hits = %d, want 1", got)
	}

	if _, err := os.Stat(filepath.Join(cfg.CacheDir, cacheFileName)); err != nil {
		t.Errorf("expected Load to write %s: %v", cacheFileName, err)
	}
}

func TestLoadTTL(t *testing.T) {
	t.Run("fresh cache skips the network", func(t *testing.T) {
		srv, hits := countingServer(t, sampleModelsDevPayload, http.StatusOK)
		withModelsDevURL(t, srv.URL)

		cfg := baseConfig(t)
		cfg.HTTPClient = srv.Client()
		writeCacheWithAge(t, cfg, sampleModelsDevPayload, time.Hour)

		cat, err := Load(context.Background(), cfg)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := atomic.LoadInt32(hits); got != 0 {
			t.Errorf("hits = %d, want 0 for a fresh cache", got)
		}
		if cat.Source() != "models.dev" {
			t.Errorf("Source() = %q, want %q", cat.Source(), "models.dev")
		}
	})

	t.Run("expired cache triggers exactly one refresh", func(t *testing.T) {
		srv, hits := countingServer(t, sampleModelsDevPayload, http.StatusOK)
		withModelsDevURL(t, srv.URL)

		cfg := baseConfig(t)
		cfg.HTTPClient = srv.Client()
		writeCacheWithAge(t, cfg, sampleModelsDevPayload, 25*time.Hour)

		cat, err := Load(context.Background(), cfg)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := atomic.LoadInt32(hits); got != 1 {
			t.Errorf("hits = %d, want exactly 1 for an expired cache", got)
		}
		if cat.Source() != "models.dev" {
			t.Errorf("Source() = %q, want %q", cat.Source(), "models.dev")
		}
	})
}

func TestLoadDegradation(t *testing.T) {
	t.Run("500 and no cache falls back to embedded without an error", func(t *testing.T) {
		srv, _ := countingServer(t, "", http.StatusInternalServerError)
		withModelsDevURL(t, srv.URL)

		cfg := baseConfig(t)
		cfg.HTTPClient = srv.Client()

		cat, err := Load(context.Background(), cfg)
		if err != nil {
			t.Fatalf("Load must never error on a network failure, got: %v", err)
		}
		if cat.Source() != "embedded" {
			t.Errorf("Source() = %q, want %q", cat.Source(), "embedded")
		}
	})

	t.Run("500 with a stale-but-valid cache keeps serving it", func(t *testing.T) {
		srv, _ := countingServer(t, "", http.StatusInternalServerError)
		withModelsDevURL(t, srv.URL)

		cfg := baseConfig(t)
		cfg.HTTPClient = srv.Client()
		writeCacheWithAge(t, cfg, sampleModelsDevPayload, 25*time.Hour)

		cat, err := Load(context.Background(), cfg)
		if err != nil {
			t.Fatalf("Load must never error, got: %v", err)
		}
		if cat.Source() != "models.dev" {
			t.Errorf("Source() = %q, want %q (stale cache should still serve)", cat.Source(), "models.dev")
		}
		if _, ok := cat.Lookup("tool", "claude-opus-5"); !ok {
			t.Error("expected the stale cache's model to still resolve")
		}
	})

	t.Run("corrupt cache is ignored and falls through", func(t *testing.T) {
		// Unreachable server: any dial fails, simulating "no network" so
		// the corrupt cache has nowhere to recover from but embedded.
		cfg := baseConfig(t)
		withModelsDevURL(t, "http://127.0.0.1:0")

		if err := os.MkdirAll(cfg.CacheDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		path := filepath.Join(cfg.CacheDir, cacheFileName)
		if err := os.WriteFile(path, []byte("not valid json at all"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		cat, err := Load(context.Background(), cfg)
		if err != nil {
			t.Fatalf("Load must never error on a corrupt cache, got: %v", err)
		}
		if cat.Source() != "embedded" {
			t.Errorf("Source() = %q, want %q", cat.Source(), "embedded")
		}
	})
}

func TestLoadOffline(t *testing.T) {
	t.Run("no cache stays on embedded with zero requests", func(t *testing.T) {
		srv, hits := countingServer(t, sampleModelsDevPayload, http.StatusOK)
		withModelsDevURL(t, srv.URL)

		cfg := baseConfig(t)
		cfg.HTTPClient = srv.Client()
		cfg.Offline = true

		cat, err := Load(context.Background(), cfg)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := atomic.LoadInt32(hits); got != 0 {
			t.Errorf("hits = %d, want 0 when offline", got)
		}
		if cat.Source() != "embedded" {
			t.Errorf("Source() = %q, want %q", cat.Source(), "embedded")
		}
	})

	t.Run("stale cache still serves without a request", func(t *testing.T) {
		srv, hits := countingServer(t, sampleModelsDevPayload, http.StatusOK)
		withModelsDevURL(t, srv.URL)

		cfg := baseConfig(t)
		cfg.HTTPClient = srv.Client()
		cfg.Offline = true
		writeCacheWithAge(t, cfg, sampleModelsDevPayload, 25*time.Hour)

		cat, err := Load(context.Background(), cfg)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := atomic.LoadInt32(hits); got != 0 {
			t.Errorf("hits = %d, want 0 when offline", got)
		}
		if cat.Source() != "models.dev" {
			t.Errorf("Source() = %q, want %q", cat.Source(), "models.dev")
		}
	})
}

func TestCachePathAndWriteCacheWithNoCacheDir(t *testing.T) {
	cfg := Config{}
	if got := cachePath(cfg); got != "" {
		t.Errorf("cachePath with no CacheDir = %q, want empty", got)
	}
	if err := writeCache(cfg, []byte("irrelevant")); err != nil {
		t.Errorf("writeCache with no CacheDir should be a no-op, got: %v", err)
	}
	if _, _, ok := readCache(cfg); ok {
		t.Error("readCache with no CacheDir should report false")
	}
}

func TestReadCacheUnreadableFile(t *testing.T) {
	cfg := baseConfig(t)
	// A directory where a file is expected makes os.ReadFile fail even
	// though os.Stat succeeds, exercising the read-error branch distinct
	// from "file absent".
	if err := os.MkdirAll(filepath.Join(cfg.CacheDir, cacheFileName), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if _, _, ok := readCache(cfg); ok {
		t.Error("expected readCache to report false for an unreadable cache path")
	}
}

func TestResolveRemoteMalformedResponse(t *testing.T) {
	t.Run("no cache to fall back to", func(t *testing.T) {
		srv, _ := countingServer(t, "not valid json", http.StatusOK)
		withModelsDevURL(t, srv.URL)

		cfg := baseConfig(t)
		cfg.HTTPClient = srv.Client()

		if got := resolveRemote(context.Background(), cfg); got != nil {
			t.Errorf("resolveRemote = %v, want nil", got)
		}
	})

	t.Run("falls back to a stale but valid cache", func(t *testing.T) {
		srv, _ := countingServer(t, "not valid json", http.StatusOK)
		withModelsDevURL(t, srv.URL)

		cfg := baseConfig(t)
		cfg.HTTPClient = srv.Client()
		writeCacheWithAge(t, cfg, sampleModelsDevPayload, 25*time.Hour)

		got := resolveRemote(context.Background(), cfg)
		if got == nil {
			t.Fatal("expected resolveRemote to fall back to the stale cache")
		}
		if _, ok := got.lookup("tool", "claude-opus-5"); !ok {
			t.Error("expected the stale cache's model to resolve")
		}
	})
}

// writeCacheWithAge writes body as the cache file and backdates its mtime
// by age, so TTL logic can be exercised deterministically instead of
// sleeping in a test.
func writeCacheWithAge(t *testing.T, cfg Config, body string, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(cfg.CacheDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(cfg.CacheDir, cacheFileName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	mtime := time.Now().Add(-age)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
}
