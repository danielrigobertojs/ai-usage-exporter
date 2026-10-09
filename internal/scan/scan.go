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
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/aggregate"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider"
)

type scanIDKey struct{}

// WithID attaches a correlation identifier to every log emitted by Run.
func WithID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, scanIDKey{}, id)
}
func idFrom(ctx context.Context) string {
	if id, ok := ctx.Value(scanIDKey{}).(string); ok {
		return id
	}
	return "unknown"
}

// ID returns the scan correlation ID, or "unknown" for contexts not created
// with WithID. It lets callers correlate publication with Run's terminal log.
func ID(ctx context.Context) string { return idFrom(ctx) }

// ToolResult reports what Run observed for one provider during a single
// scan, independent of whether any of its events survived aggregation.
type ToolResult struct {
	Available            bool // at least one source was found and at least one read fully
	FilesScanned         int
	FilesSkipped         int
	FilesSkippedBySize   int
	FilesSkippedByType   int
	FilesSkippedByBudget int
	ParseErrors          int
	FirstParseError      string
	BudgetHit            bool
	ResolvedRoots        []string
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
// Run returns an error when its context is cancelled before or during the
// scan. Callers must not publish the partial aggregate in that case.
func Run(ctx context.Context, reg *provider.Registry, env provider.Env, b provider.Budget, now time.Time, tz *time.Location) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	start := time.Now()
	scanID := idFrom(ctx)
	logger := slog.Default().With("scan_id", scanID)
	logger.Info("scan started", "providers", len(reg.All()))
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
	_ = g.Wait() // workers deliberately return nil; provider failures are per-tool.
	if err := ctx.Err(); err != nil {
		logger.Error("scan cancelled", "error_type", fmt.Sprintf("%T", err))
		return Result{}, err
	}
	snapshot := agg.Snapshot()
	logger.Info("scan complete", "duration", time.Since(start), "tools", len(perTool), "events_duplicates", snapshot.Duplicates, "events_invalid", snapshot.Invalid)
	if snapshot.Duplicates > 0 {
		logger.Debug("duplicate events discarded", "count", snapshot.Duplicates)
	}

	return Result{
		Snapshot: snapshot,
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
	logger := slog.Default().With("scan_id", idFrom(ctx), "tool", d.ID)
	logger.Debug("provider discovery started")

	sources, stats, err := provider.Discover(ctx, d, env, b)
	if err != nil {
		logger.Error("provider discovery failed", "error_type", fmt.Sprintf("%T", err))
		return ToolResult{ParseErrors: 1}
	}
	logger.Info("provider discovery complete", "files", len(sources), "skipped", stats.FilesSkipped)
	for _, root := range stats.ResolvedRoots {
		logger.Debug("provider root resolved", "root", root)
	}
	if stats.FilesSkipped > 0 {
		logger.Info("sources skipped", "count", stats.FilesSkipped, "size", stats.FilesSkippedBySize, "type", stats.FilesSkippedByType, "budget", stats.FilesSkippedByBudget)
	}
	if stats.BudgetHit {
		logger.Warn("scan budget exhausted", "files", len(sources))
	}

	tr := ToolResult{
		FilesScanned:         len(sources),
		FilesSkipped:         stats.FilesSkipped,
		FilesSkippedBySize:   stats.FilesSkippedBySize,
		FilesSkippedByType:   stats.FilesSkippedByType,
		FilesSkippedByBudget: stats.FilesSkippedByBudget,
		BudgetHit:            stats.BudgetHit,
		ResolvedRoots:        stats.ResolvedRoots,
	}

	successes := 0
	cancelled := 0
	for i, src := range sources {
		started := time.Now()
		parseErr := p.Parse(ctx, src, func(e model.UsageEvent) error {
			mu.Lock()
			agg.Add(e)
			mu.Unlock()
			return nil
		})
		if parseErr != nil {
			if ctx.Err() != nil || parseErr == context.Canceled || parseErr == context.DeadlineExceeded {
				cancelled++
			} else {
				logger.Warn("source parse failed", "source", i+1, "error_type", fmt.Sprintf("%T", parseErr))
			}
			tr.ParseErrors++
			if tr.FirstParseError == "" {
				tr.FirstParseError = parseErr.Error()
			}
			continue
		}
		logger.Debug("source read", "source", i+1, "path", src.Path, "bytes", src.Size, "duration", time.Since(started))
		successes++
	}
	if cancelled > 0 {
		logger.Warn("source parsing cancelled", "sources", cancelled, "error_type", fmt.Sprintf("%T", ctx.Err()))
	}
	tr.Available = successes > 0

	return tr
}
