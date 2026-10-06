// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

// Package scan runs every registered provider once, in parallel, and folds
// their events into a single aggregate.Snapshot - the one scan ADR-001
// commits this exporter to: there is no re-scan loop inside this package,
// only a single Run per call, so cmd/ is the only place that decides
// whether or when to call it again (SIGHUP, scan_interval).
package scan

import (
	"context"
	"runtime"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/aggregate"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider"
)

// ToolResult reports what Run observed for one provider during a single
// scan, independent of whether any of its events survived aggregation.
type ToolResult struct {
	Available    bool // at least one source was found and at least one read fully
	FilesScanned int
	FilesSkipped int
	ParseErrors  int
	BudgetHit    bool
}

// Result is everything a single call to Run produces. It is immutable once
// returned: Snapshot's own maps are already independent copies (see
// aggregate.Aggregator.Snapshot), and PerTool is built fresh per call.
type Result struct {
	Snapshot aggregate.Snapshot
	PerTool  map[string]ToolResult
	Duration time.Duration
}

// Run scans every provider in reg in parallel - one goroutine per provider,
// bounded by an errgroup limited to GOMAXPROCS - and folds their events
// into a single aggregate.Aggregator guarded by a mutex. now and tz are
// forwarded to aggregate.New unchanged: Run never reads the clock itself,
// so two calls given the same inputs produce the same windows.
//
// A provider that fails - Discover errors, or Parse fails on some or all
// of its sources - never aborts the others: its failure is folded into its
// own ToolResult.ParseErrors/Available instead of being returned from Run.
// Run's error return is reserved for ctx already being done when it is
// called.
func Run(ctx context.Context, reg *provider.Registry, env provider.Env, b provider.Budget, now time.Time, tz *time.Location) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	start := time.Now()
	agg := aggregate.New(now, tz)
	var aggMu sync.Mutex

	providers := reg.All()

	var resultsMu sync.Mutex
	perTool := make(map[string]ToolResult, len(providers))

	g, gctx := errgroup.WithContext(ctx)
	if n := runtime.GOMAXPROCS(0); n > 0 {
		g.SetLimit(n)
	}

	for _, p := range providers {
		p := p
		g.Go(func() error {
			id := p.Descriptor().ID
			tr := scanProvider(gctx, p, env, b, agg, &aggMu)

			resultsMu.Lock()
			perTool[id] = tr
			resultsMu.Unlock()

			// Never propagate a per-provider failure as a group error: it is
			// already recorded in tr, and an errgroup error would cancel
			// gctx for every provider still running.
			return nil
		})
	}
	_ = g.Wait()

	return Result{
		Snapshot: agg.Snapshot(),
		PerTool:  perTool,
		Duration: time.Since(start),
	}, nil
}

// scanProvider discovers and parses every source for one provider, folding
// its events into agg under mu. Available only turns true once at least one
// source was both found and fully parsed - a tool that is simply not
// installed (zero sources) is not the same failure mode as one whose
// sources all failed to parse, but docs/metrics.md collapses both to 0 by
// design: distinguishing them further belongs in logs, not in a label.
func scanProvider(ctx context.Context, p provider.Provider, env provider.Env, b provider.Budget, agg *aggregate.Aggregator, mu *sync.Mutex) ToolResult {
	d := p.Descriptor()

	sources, stats, err := provider.Discover(ctx, d, env, b)
	if err != nil {
		return ToolResult{ParseErrors: 1}
	}

	tr := ToolResult{
		FilesScanned: len(sources),
		FilesSkipped: stats.FilesSkipped,
		BudgetHit:    stats.BudgetHit,
	}

	successes := 0
	for _, src := range sources {
		parseErr := p.Parse(ctx, src, func(e model.UsageEvent) error {
			mu.Lock()
			agg.Add(e)
			mu.Unlock()
			return nil
		})
		if parseErr != nil {
			tr.ParseErrors++
			continue
		}
		successes++
	}
	tr.Available = successes > 0

	return tr
}
