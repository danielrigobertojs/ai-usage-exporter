// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

// Package collector turns a scan.Result into the eleven metrics
// docs/metrics.md contracts: a prometheus.Collector that serves a single,
// atomically-swappable snapshot rather than recomputing anything on scrape,
// per ADR-001.
package collector

import (
	"log/slog"
	"runtime"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/aggregate"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/pricing"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/scan"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/version"
)

const namespace = "ai_usage"

// Options controls the opt-in, off-by-default knobs the cardinality budget
// in docs/metrics.md requires. A zero Options is the safe default: no
// project label, no extra series.
type Options struct {
	// ProjectLabel is reserved for a per-project series dimension. It is
	// deliberately not wired into Collect yet: aggregate.Snapshot carries no
	// per-project data today (model.UsageEvent.ProjectID is dropped by
	// aggregate.Aggregator.Add), so there is nothing to label even when this
	// is true. NormalizeProjectLabel below is tested standalone so the
	// cardinality guard (truncate to MaxProjectLabel, never emit when
	// disabled) is ready the moment that dimension exists.
	ProjectLabel    bool
	MaxProjectLabel int
}

// Collector implements prometheus.Collector over a scan.Result stored
// atomically: Describe is static, Collect always reads whatever Set last
// published. It never re-scans and never blocks a Collect on I/O.
type Collector struct {
	cat  pricing.Catalog
	opts Options

	mu          sync.RWMutex
	result      scan.Result
	ready       bool
	scanSuccess bool

	tokens             *prometheus.Desc
	costUSD            *prometheus.Desc
	sessions           *prometheus.Desc
	toolCalls          *prometheus.Desc
	lastEventTimestamp *prometheus.Desc
	providerAvailable  *prometheus.Desc
	scanTimestamp      *prometheus.Desc
	scanDuration       *prometheus.Desc
	scanFiles          *prometheus.Desc
	scanErrors         *prometheus.Desc
	buildInfo          *prometheus.Desc
	scanSuccessMetric  *prometheus.Desc
}

// New returns a Collector that prices events against cat and applies opts.
// A zero-value opts.MaxProjectLabel is replaced with DefaultMaxProjectLabel.
func New(cat pricing.Catalog, opts Options) *Collector {
	if opts.MaxProjectLabel <= 0 {
		opts.MaxProjectLabel = DefaultMaxProjectLabel
	}

	return &Collector{
		cat:  cat,
		opts: opts,

		tokens: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "tokens"),
			"Number of token_type tokens consumed by model within tool, aggregated over window, per the local history available at scan time. Deduplicated by message id.",
			[]string{"tool", "model", "token_type", "window"}, nil,
		),
		costUSD: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "cost_usd"),
			"Estimated cost in USD attributable to model within tool over window, derived from token counts and the pricing catalog in effect at scan time. Absent for a (tool, model) pair the catalog has no price for.",
			[]string{"tool", "model", "window"}, nil,
		),
		sessions: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "sessions"),
			"Number of distinct tool sessions with at least one event within window. A resumed or forked session counts once.",
			[]string{"tool", "window"}, nil,
		),
		toolCalls: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "tool_calls"),
			"Number of tool/function calls recorded by tool within window.",
			[]string{"tool", "window"}, nil,
		),
		lastEventTimestamp: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "last_event_timestamp_seconds"),
			"Unix timestamp (UTC) of the most recent event found in tool's local history at scan time.",
			[]string{"tool"}, nil,
		),
		providerAvailable: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "provider_available"),
			"1 if tool's data source was found and read in this scan, 0 if it does not exist, is not readable, or parsing failed completely.",
			[]string{"tool"}, nil,
		),
		scanTimestamp: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "scan_timestamp_seconds"),
			"Unix timestamp (UTC) at which the scan serving the current /metrics snapshot completed.",
			nil, nil,
		),
		scanDuration: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "scan_duration_seconds"),
			"How long the startup scan of every provider took.",
			nil, nil,
		),
		scanFiles: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "scan_files"),
			"Number of files (or rows/sessions, for SQLite-backed providers) tool's provider read in this scan.",
			[]string{"tool"}, nil,
		),
		scanErrors: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "scan_errors"),
			"Number of files or records tool's provider failed to parse in this scan.",
			[]string{"tool"}, nil,
		),
		buildInfo: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "build_info"),
			"Build metadata of the running binary. Value is always 1; the information is in the labels.",
			[]string{"version", "commit", "go_version"}, nil,
		),
		scanSuccessMetric: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "scan_success"),
			"1 if the most recent scan completed and its snapshot is safe to use; 0 if it failed or was cancelled.",
			nil, nil,
		),
	}
}

// Set atomically replaces the Result Collect serves. Safe to call
// concurrently with Collect.
func (c *Collector) Set(r scan.Result) {
	for key := range r.Snapshot.Tokens {
		if key.Window == aggregate.WindowAll {
			if _, ok := c.cat.Lookup(key.Tool, key.Model); !ok {
				slog.Warn("model has no pricing rate", "tool", key.Tool, "model", key.Model)
			}
		}
	}
	c.mu.Lock()
	c.result = r
	c.ready = true
	c.scanSuccess = true
	c.mu.Unlock()
}

// MarkScanFailure records a failed scan without replacing a previously safe
// snapshot. Before the first successful scan this leaves usage metrics absent.
func (c *Collector) MarkScanFailure() {
	c.mu.Lock()
	c.scanSuccess = false
	c.mu.Unlock()
}

// Ready reports whether Set has been called at least once.
func (c *Collector) Ready() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ready
}

// Catalog returns the immutable pricing catalog used by this collector.
func (c *Collector) Catalog() pricing.Catalog { return c.cat }

// TotalCostUSD returns the aggregate estimated cost for the all-history
// window. Other windows overlap and must not be added together.
func TotalCostUSD(cat pricing.Catalog, tokens map[aggregate.TokenKey]int64) float64 {
	var total float64
	for key, count := range tokens {
		if key.Window != aggregate.WindowAll {
			continue
		}
		rates, ok := cat.Lookup(key.Tool, key.Model)
		if !ok {
			continue
		}
		total += float64(count) * rateFor(key.Class, rates)
	}
	return total
}

// Describe sends every metric Collect can possibly emit, independent of
// whether Set has been called yet.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.tokens
	ch <- c.costUSD
	ch <- c.sessions
	ch <- c.toolCalls
	ch <- c.lastEventTimestamp
	ch <- c.providerAvailable
	ch <- c.scanTimestamp
	ch <- c.scanDuration
	ch <- c.scanFiles
	ch <- c.scanErrors
	ch <- c.buildInfo
	ch <- c.scanSuccessMetric
}

// Collect reads the currently published Result and emits it as constant
// metrics. Called with no Result ever Set, it emits only build_info - the
// server's /readyz, not Collect, is what tells Prometheus not to scrape yet.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	c.mu.RLock()
	result := c.result
	ready := c.ready
	scanSuccess := c.scanSuccess
	c.mu.RUnlock()

	ch <- prometheus.MustNewConstMetric(c.buildInfo, prometheus.GaugeValue, 1,
		version.Version, version.Commit, runtime.Version())
	if scanSuccess {
		ch <- prometheus.MustNewConstMetric(c.scanSuccessMetric, prometheus.GaugeValue, 1)
	} else {
		ch <- prometheus.MustNewConstMetric(c.scanSuccessMetric, prometheus.GaugeValue, 0)
	}

	if !ready {
		return
	}

	snap := result.Snapshot

	for k, v := range snap.Tokens {
		ch <- prometheus.MustNewConstMetric(c.tokens, prometheus.GaugeValue, float64(v),
			k.Tool, k.Model, string(k.Class), string(k.Window))
	}

	for ck, v := range costPerModelWindow(c.cat, snap.Tokens) {
		ch <- prometheus.MustNewConstMetric(c.costUSD, prometheus.GaugeValue, v,
			ck.Tool, ck.Model, string(ck.Window))
	}

	for k, v := range snap.Sessions {
		ch <- prometheus.MustNewConstMetric(c.sessions, prometheus.GaugeValue, float64(v),
			k.Tool, string(k.Window))
	}

	for k, v := range snap.ToolCalls {
		ch <- prometheus.MustNewConstMetric(c.toolCalls, prometheus.GaugeValue, float64(v),
			k.Tool, string(k.Window))
	}

	for tool, ts := range snap.LastEventAt {
		ch <- prometheus.MustNewConstMetric(c.lastEventTimestamp, prometheus.GaugeValue,
			float64(ts.Unix()), tool)
	}

	ch <- prometheus.MustNewConstMetric(c.scanTimestamp, prometheus.GaugeValue, float64(snap.ScannedAt.Unix()))
	ch <- prometheus.MustNewConstMetric(c.scanDuration, prometheus.GaugeValue, result.Duration.Seconds())

	for tool, tr := range result.PerTool {
		available := 0.0
		if tr.Available {
			available = 1.0
		}
		ch <- prometheus.MustNewConstMetric(c.providerAvailable, prometheus.GaugeValue, available, tool)
		ch <- prometheus.MustNewConstMetric(c.scanFiles, prometheus.GaugeValue, float64(tr.FilesScanned), tool)
		ch <- prometheus.MustNewConstMetric(c.scanErrors, prometheus.GaugeValue, float64(tr.ParseErrors), tool)
	}
}

// costKey identifies one ai_usage_cost_usd series.
type costKey struct {
	Tool, Model string
	Window      aggregate.Window
}

// costPerModelWindow re-prices every (tool, model, window) group straight
// from the catalog: there is no native-cost field on model.UsageEvent to
// reconcile against, so the catalog is the only source ai_usage_cost_usd
// can ever come from - this is what "the catalog always wins over a
// provider's native cost" means in code (see docs/metrics.md). A
// (tool, model) pair the catalog has no rates for is left out of the
// returned map entirely: an absent series, never a $0 one.
func costPerModelWindow(cat pricing.Catalog, tokens map[aggregate.TokenKey]int64) map[costKey]float64 {
	costs := make(map[costKey]float64)
	for tk, count := range tokens {
		if count == 0 {
			continue
		}
		rates, ok := cat.Lookup(tk.Tool, tk.Model)
		if !ok {
			continue
		}
		ck := costKey{Tool: tk.Tool, Model: tk.Model, Window: tk.Window}
		costs[ck] += float64(count) * rateFor(tk.Class, rates)
	}
	return costs
}

func rateFor(class model.TokenClass, r pricing.Rates) float64 {
	switch class {
	case model.TokenInput:
		return r.Input
	case model.TokenOutput:
		return r.Output
	case model.TokenCacheRead:
		return r.CacheRead
	case model.TokenCacheWrite:
		return r.CacheWrite
	case model.TokenReasoning:
		return r.Reasoning
	default:
		return 0
	}
}
