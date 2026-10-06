// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package pricing

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// cacheFileName is versioned so a future breaking change to the cached
// shape (e.g. storing something other than the raw models.dev payload)
// can roll out without ever trying to parse an old file under the new
// rules.
const cacheFileName = "models-dev-v1.json"

func cachePath(cfg Config) string {
	if cfg.CacheDir == "" {
		return ""
	}
	return filepath.Join(cfg.CacheDir, cacheFileName)
}

// readCache returns the cached models.dev table and the cache file's
// mtime. ok is false whenever the cache can't be used as-is: absent,
// unreadable, or corrupt - callers treat all three identically and fall
// through to the next tier, never panicking or erroring on bad disk state.
func readCache(cfg Config) (t table, modTime time.Time, ok bool) {
	path := cachePath(cfg)
	if path == "" {
		return nil, time.Time{}, false
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, time.Time{}, false
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, time.Time{}, false
	}

	parsed, err := parseModelsDev(data)
	if err != nil {
		return nil, time.Time{}, false
	}

	return parsed, info.ModTime(), true
}

// writeCache persists raw (the exact bytes models.dev served) to disk via
// a write-then-rename so a crash mid-write never leaves a half-written
// file for the next readCache to trip over. Any failure here is
// best-effort: Load already has the parsed table it needs in memory and
// must not fail the whole resolution just because the cache directory
// turned out to be unwritable.
func writeCache(cfg Config, raw []byte) error {
	if cfg.CacheDir == "" {
		return nil
	}
	if err := os.MkdirAll(cfg.CacheDir, 0o755); err != nil {
		return err
	}

	path := cachePath(cfg)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// resolveRemote is the models.dev tier of Load's resolution order: serve a
// fresh cache without touching the network; otherwise, unless Offline,
// fetch and refresh the cache; and on any failure (offline, network error,
// bad payload) fall back to a still-present stale cache before giving up
// and returning nil, which tells Load to fall through to the embedded
// tier. It never returns an error - every failure mode here is a
// degradation, not a fault, per the invariant this package exists to
// enforce.
func resolveRemote(ctx context.Context, cfg Config) table {
	cached, modTime, ok := readCache(cfg)
	if ok && time.Since(modTime) < cfg.TTL {
		return cached
	}

	if cfg.Offline {
		if ok {
			return cached
		}
		return nil
	}

	raw, err := fetchModelsDevRaw(ctx, cfg)
	if err != nil {
		if ok {
			return cached
		}
		return nil
	}

	parsed, err := parseModelsDev(raw)
	if err != nil {
		if ok {
			return cached
		}
		return nil
	}

	_ = writeCache(cfg, raw)
	return parsed
}

// Load resolves a Catalog in the order overlay > models.dev (cache or
// live) > embedded. It never returns a non-nil error for a network,
// cache, or data problem - see resolveRemote and loadOverlay, which
// degrade instead of failing. The error return is reserved for an
// Load-internal invariant violation, which the current Config shape
// cannot trigger; it exists so a future required field can be validated
// without an API-breaking signature change.
func Load(ctx context.Context, cfg Config) (Catalog, error) {
	cfg = cfg.withDefaults()

	overlay := loadOverlay(cfg)
	remote := resolveRemote(ctx, cfg)

	source := "embedded"
	if remote != nil {
		source = "models.dev"
	}
	if len(overlay) > 0 {
		source = "overlay"
	}

	return &layeredCatalog{
		overlay:  overlay,
		remote:   remote,
		embedded: embeddedTable(),
		source:   source,
	}, nil
}
