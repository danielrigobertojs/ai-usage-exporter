// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package provider

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"time"

	"github.com/bmatcuk/doublestar/v4"
)

// Source is one on-disk file Discover found for a Descriptor, ready to pass
// to Provider.Parse.
type Source struct {
	Path    string
	Kind    SourceKind
	Size    int64
	ModTime time.Time
}

// Budget bounds a startup scan. Real agent session logs run 700 MB-2 GB
// (see openai/codex issues); without a cap like this, a naive scan turns
// serving the first /metrics response into a multi-minute wait.
type Budget struct {
	Deadline      time.Time
	MaxFiles      int   // 20000
	MaxBytesFile  int64 // 256 << 20
	MaxTotalBytes int64 // 4 << 30
	MaxHeaderRead int64 // 64 << 10 (reserved for Provider.Parse's own header sniffing)
}

// DefaultBudget returns the standard scan budget, deadlined 30s from now.
func DefaultBudget(now time.Time) Budget {
	return Budget{
		Deadline:      now.Add(30 * time.Second),
		MaxFiles:      20000,
		MaxBytesFile:  256 << 20,
		MaxTotalBytes: 4 << 30,
		MaxHeaderRead: 64 << 10,
	}
}

// Stats reports what a Discover call saw, independent of whether every file
// seen made it into the returned Source list.
type Stats struct {
	FilesSeen            int
	FilesSkipped         int // sum of FilesSkippedBySize, FilesSkippedByType, and FilesSkippedByBudget
	FilesSkippedBySize   int
	FilesSkippedByType   int
	FilesSkippedByBudget int
	BudgetHit            bool
	ResolvedRoots        []string
}

// Discover expands d's Roots against env, applies b, and returns the
// matching Sources ordered by ModTime descending (most recent first), so
// that an exhausted budget drops the oldest files, never the newest. A root
// that doesn't exist - a tool that isn't installed - is not an error: it
// simply contributes no sources.
func Discover(ctx context.Context, d Descriptor, env Env, b Budget) ([]Source, Stats, error) {
	if err := ctx.Err(); err != nil {
		return nil, Stats{}, err
	}
	if err := d.Validate(); err != nil {
		return nil, Stats{}, fmt.Errorf("provider: discover: %w", err)
	}

	override := resolveHomeOverride(d, env)

	var stats Stats
	resolvedRoots := make(map[string]struct{})
	var candidates []Source

	for _, spec := range d.Roots {
		if spec.GOOS != "" && spec.GOOS != env.GOOS {
			continue
		}
		base, ok := resolveBase(spec, env, override)
		if !ok {
			continue
		}
		root := path.Join(base, spec.Rel)
		resolvedRoots[toRealPath(env.GOOS, root)] = struct{}{}
		pattern := path.Join(root, spec.Glob)

		matches, err := doublestar.Glob(env.FS, pattern)
		if err != nil {
			// A malformed glob is a descriptor bug, not a disk condition;
			// skip this root rather than failing the whole scan.
			continue
		}

		for _, m := range matches {
			info, err := fs.Stat(env.FS, m)
			if err != nil {
				continue
			}
			if info.IsDir() {
				stats.FilesSkipped++
				stats.FilesSkippedByType++
				continue
			}
			stats.FilesSeen++
			// MaxBytesFile bounds how much a streaming Provider.Parse has to
			// read sequentially into memory. A SourceSQLite candidate is
			// never streamed - it's opened read-only and queried through
			// indices - so its on-disk size predicts neither the memory nor
			// the time Parse will take, and the cap does not apply to it
			// (ADR-005).
			if d.Kind != SourceSQLite && info.Size() > b.MaxBytesFile {
				stats.FilesSkipped++
				stats.FilesSkippedBySize++
				continue
			}
			candidates = append(candidates, Source{
				Path:    toRealPath(env.GOOS, m),
				Kind:    d.Kind,
				Size:    info.Size(),
				ModTime: info.ModTime(),
			})
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		ti, tj := candidates[i].ModTime, candidates[j].ModTime
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return candidates[i].Path < candidates[j].Path
	})

	sources := make([]Source, 0, len(candidates))
	var totalBytes int64
	cutoff := false
	for _, c := range candidates {
		// A SourceSQLite candidate's bytes never count against
		// MaxTotalBytes, for the same reason MaxBytesFile doesn't apply to
		// it above: it isn't read in full, so its size isn't a cost this
		// budget should be spent on. Counting it would let one large
		// opencode.db starve every other provider's streaming budget in the
		// same scan (ADR-005).
		countsTowardTotal := c.Kind != SourceSQLite
		switch {
		case cutoff:
		case time.Now().After(b.Deadline):
			stats.BudgetHit = true
			cutoff = true
		case b.MaxFiles > 0 && len(sources) >= b.MaxFiles:
			stats.BudgetHit = true
			cutoff = true
		case countsTowardTotal && b.MaxTotalBytes > 0 && totalBytes+c.Size > b.MaxTotalBytes:
			stats.BudgetHit = true
			cutoff = true
		}
		if cutoff {
			stats.FilesSkipped++
			stats.FilesSkippedByBudget++
			continue
		}
		sources = append(sources, c)
		if countsTowardTotal {
			totalBytes += c.Size
		}
	}
	for root := range resolvedRoots {
		stats.ResolvedRoots = append(stats.ResolvedRoots, root)
	}
	sort.Strings(stats.ResolvedRoots)

	return sources, stats, nil
}
