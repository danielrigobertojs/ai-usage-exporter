// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package collector

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/prometheus/client_golang/prometheus/testutil/promlint"
	dto "github.com/prometheus/client_model/go"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/aggregate"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/license"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/pricing"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/scan"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/version"
)

// fakeCatalog answers Lookup for exactly the (tool, model) pairs tests
// register, so cost math is fully deterministic without touching the real
// embedded table.
type fakeCatalog struct {
	rates map[[2]string]pricing.Rates
}

func (f fakeCatalog) Lookup(tool, modelID string) (pricing.Rates, bool) {
	r, ok := f.rates[[2]string{tool, modelID}]
	return r, ok
}

func (f fakeCatalog) Source() string { return "fake" }

func fixedResult() scan.Result {
	scannedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return scan.Result{
		Snapshot: aggregate.Snapshot{
			ScannedAt: scannedAt,
			Tokens: map[aggregate.TokenKey]int64{
				{Tool: "claude-code", Model: "claude-opus-4", Class: model.TokenInput, Window: aggregate.WindowAll}:  100,
				{Tool: "claude-code", Model: "claude-opus-4", Class: model.TokenOutput, Window: aggregate.WindowAll}: 50,
				{Tool: "claude-code", Model: "unknown-model", Class: model.TokenInput, Window: aggregate.WindowAll}:  7,
			},
			Sessions: map[aggregate.ScopeKey]int64{
				{Tool: "claude-code", Window: aggregate.WindowAll}: 3,
			},
			ToolCalls: map[aggregate.ScopeKey]int64{
				{Tool: "claude-code", Window: aggregate.WindowAll}: 9,
			},
			LastEventAt: map[string]time.Time{
				"claude-code": scannedAt,
			},
		},
		PerTool: map[string]scan.ToolResult{
			"claude-code": {Available: true, FilesScanned: 4, ParseErrors: 1},
		},
		Duration: 2500 * time.Millisecond,
	}
}

func fixedCatalog() pricing.Catalog {
	return fakeCatalog{rates: map[[2]string]pricing.Rates{
		{"claude-code", "claude-opus-4"}: {Input: 0.01, Output: 0.02},
	}}
}

// mustGather registers c on a fresh Registry and returns the gathered
// MetricFamily protos - a structured alternative to parsing text output,
// used by the tests that need to inspect individual label values.
func mustGather(t *testing.T, c *Collector) []*dto.MetricFamily {
	t.Helper()
	reg := prometheus.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("Register: %v", err)
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	return families
}

func findFamily(families []*dto.MetricFamily, name string) *dto.MetricFamily {
	for _, mf := range families {
		if mf.GetName() == name {
			return mf
		}
	}
	return nil
}

func labelsMatch(m *dto.Metric, want map[string]string) bool {
	if len(m.Label) != len(want) {
		return false
	}
	for _, lp := range m.Label {
		if want[lp.GetName()] != lp.GetValue() {
			return false
		}
	}
	return true
}

func hasSeries(families []*dto.MetricFamily, name string, labels map[string]string) bool {
	mf := findFamily(families, name)
	if mf == nil {
		return false
	}
	for _, m := range mf.Metric {
		if labelsMatch(m, labels) {
			return true
		}
	}
	return false
}

func hasSeriesWithLabel(families []*dto.MetricFamily, name, labelName, labelValue string) bool {
	mf := findFamily(families, name)
	if mf == nil {
		return false
	}
	for _, m := range mf.Metric {
		for _, lp := range m.Label {
			if lp.GetName() == labelName && lp.GetValue() == labelValue {
				return true
			}
		}
	}
	return false
}

func gaugeValue(families []*dto.MetricFamily, name string, labels map[string]string) float64 {
	mf := findFamily(families, name)
	if mf == nil {
		return -1
	}
	for _, m := range mf.Metric {
		if labelsMatch(m, labels) {
			return m.GetGauge().GetValue()
		}
	}
	return -1
}

// TestCollectMatchesMetricsContract covers step 6: the collector emits
// exactly the 11 metrics of docs/metrics.md, with the exact names, labels,
// types, and values a fixed Result implies.
func TestCollectMatchesMetricsContract(t *testing.T) {
	c := New(fixedCatalog(), Options{})
	c.Set(fixedResult())

	expected := fmt.Sprintf(`
# HELP ai_usage_build_info Build metadata of the running binary. Value is always 1; the information is in the labels.
# TYPE ai_usage_build_info gauge
ai_usage_build_info{commit="%s",go_version="%s",license="%s",project="%s",version="%s"} 1
# HELP ai_usage_cost_usd Estimated cost in USD attributable to model within tool over window, derived from token counts and the pricing catalog in effect at scan time. Absent for a (tool, model) pair the catalog has no price for.
# TYPE ai_usage_cost_usd gauge
ai_usage_cost_usd{model="claude-opus-4",tool="claude-code",window="all"} 2
# HELP ai_usage_last_event_timestamp_seconds Unix timestamp (UTC) of the most recent event found in tool's local history at scan time.
# TYPE ai_usage_last_event_timestamp_seconds gauge
ai_usage_last_event_timestamp_seconds{tool="claude-code"} 1.767225600e+09
# HELP ai_usage_provider_available 1 if tool's data source was found and read in this scan, 0 if it does not exist, is not readable, or parsing failed completely.
# TYPE ai_usage_provider_available gauge
ai_usage_provider_available{tool="claude-code"} 1
# HELP ai_usage_provider_stale 1 if tool's source was read but its usage is not current, for the given reason; 0 otherwise. Emitted only for available providers.
# TYPE ai_usage_provider_stale gauge
ai_usage_provider_stale{reason="last_event_too_old",tool="claude-code"} 0
ai_usage_provider_stale{reason="no_usage_events",tool="claude-code"} 0
ai_usage_provider_stale{reason="source_newer_than_last_event",tool="claude-code"} 0
# HELP ai_usage_scan_duration_seconds How long the startup scan of every provider took.
# TYPE ai_usage_scan_duration_seconds gauge
ai_usage_scan_duration_seconds 2.5
# HELP ai_usage_scan_errors Number of files or records tool's provider failed to parse in this scan.
# TYPE ai_usage_scan_errors gauge
ai_usage_scan_errors{tool="claude-code"} 1
# HELP ai_usage_scan_files Number of files (or rows/sessions, for SQLite-backed providers) tool's provider read in this scan.
# TYPE ai_usage_scan_files gauge
ai_usage_scan_files{tool="claude-code"} 4
# HELP ai_usage_scan_timestamp_seconds Unix timestamp (UTC) at which the scan serving the current /metrics snapshot completed.
# TYPE ai_usage_scan_timestamp_seconds gauge
ai_usage_scan_timestamp_seconds 1.7672256e+09
# HELP ai_usage_sessions Number of distinct tool sessions with at least one event within window. A resumed or forked session counts once.
# TYPE ai_usage_sessions gauge
ai_usage_sessions{tool="claude-code",window="1h"} 0
ai_usage_sessions{tool="claude-code",window="24h"} 0
ai_usage_sessions{tool="claude-code",window="30d"} 0
ai_usage_sessions{tool="claude-code",window="7d"} 0
ai_usage_sessions{tool="claude-code",window="all"} 3
ai_usage_sessions{tool="claude-code",window="mtd"} 0
# HELP ai_usage_tokens Number of token_type tokens consumed by model within tool, aggregated over window, per the local history available at scan time. Deduplicated by message id.
# TYPE ai_usage_tokens gauge
ai_usage_tokens{model="claude-opus-4",token_type="input",tool="claude-code",window="all"} 100
ai_usage_tokens{model="claude-opus-4",token_type="output",tool="claude-code",window="all"} 50
ai_usage_tokens{model="unknown-model",token_type="input",tool="claude-code",window="all"} 7
# HELP ai_usage_tool_calls Number of tool/function calls recorded by tool within window.
# TYPE ai_usage_tool_calls gauge
ai_usage_tool_calls{tool="claude-code",window="1h"} 0
ai_usage_tool_calls{tool="claude-code",window="24h"} 0
ai_usage_tool_calls{tool="claude-code",window="30d"} 0
ai_usage_tool_calls{tool="claude-code",window="7d"} 0
ai_usage_tool_calls{tool="claude-code",window="all"} 9
ai_usage_tool_calls{tool="claude-code",window="mtd"} 0
	`, version.Commit, runtime.Version(), license.License, license.Project, version.Version)

	metricNames := []string{
		"ai_usage_tokens",
		"ai_usage_cost_usd",
		"ai_usage_sessions",
		"ai_usage_tool_calls",
		"ai_usage_last_event_timestamp_seconds",
		"ai_usage_provider_available",
		"ai_usage_provider_stale",
		"ai_usage_scan_timestamp_seconds",
		"ai_usage_scan_duration_seconds",
		"ai_usage_scan_files",
		"ai_usage_scan_errors",
		"ai_usage_build_info",
	}

	if err := testutil.CollectAndCompare(c, strings.NewReader(expected), metricNames...); err != nil {
		t.Fatalf("CollectAndCompare:\n%v", err)
	}
}

func TestBuildInfoIsSingleCompleteSeries(t *testing.T) {
	c := New(fixedCatalog(), Options{})
	mf := findFamily(mustGather(t, c), "ai_usage_build_info")
	if mf == nil {
		t.Fatal("ai_usage_build_info metric family is absent")
	}
	if got := len(mf.Metric); got != 1 {
		t.Fatalf("ai_usage_build_info series = %d, want 1", got)
	}
	want := map[string]string{
		"version":    version.Version,
		"commit":     version.Commit,
		"go_version": runtime.Version(),
		"project":    license.Project,
		"license":    license.License,
	}
	if !labelsMatch(mf.Metric[0], want) {
		t.Errorf("ai_usage_build_info labels = %#v, want %#v", mf.Metric[0].Label, want)
	}
}

// TestBuildInfoProjectIsNotTheOptInUsageLabel keeps build_info's static
// project label independent from the opt-in usage-series setting.
func TestBuildInfoProjectIsNotTheOptInUsageLabel(t *testing.T) {
	c := New(fixedCatalog(), Options{ProjectLabel: true})
	mf := findFamily(mustGather(t, c), "ai_usage_build_info")
	if mf == nil || len(mf.Metric) != 1 {
		t.Fatalf("ai_usage_build_info series = %d, want 1", len(mf.GetMetric()))
	}

	if !labelsMatch(mf.Metric[0], map[string]string{
		"version":    version.Version,
		"commit":     version.Commit,
		"go_version": runtime.Version(),
		"project":    license.Project,
		"license":    license.License,
	}) {
		t.Errorf("ai_usage_build_info labels = %#v, want fixed project %q", mf.Metric[0].Label, license.Project)
	}
}

func TestProviderStaleStateSet(t *testing.T) {
	tests := []struct {
		name       string
		result     scan.Result
		wantSeries int
		wantStale  string
		wantAvail  float64
	}{
		{
			name:       "available provider affirms healthy reasons",
			result:     fixedResult(),
			wantSeries: 3,
			wantAvail:  1,
		},
		{
			name: "available provider sets exactly its stale reason",
			result: scan.Result{PerTool: map[string]scan.ToolResult{
				"opencode": {Available: true, StaleReason: "source_newer_than_last_event"},
			}},
			wantSeries: 3,
			wantStale:  "source_newer_than_last_event",
			wantAvail:  1,
		},
		{
			name: "unavailable provider has no stale state set",
			result: scan.Result{PerTool: map[string]scan.ToolResult{
				"codex": {Available: false},
			}},
			wantSeries: 0,
			wantAvail:  0,
		},
		{
			name: "source read with zero events is visible",
			result: scan.Result{PerTool: map[string]scan.ToolResult{
				"opencode": {Available: true, StaleReason: "no_usage_events"},
			}},
			wantSeries: 3,
			wantStale:  "no_usage_events",
			wantAvail:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New(fixedCatalog(), Options{})
			c.Set(tt.result)
			families := mustGather(t, c)
			mf := findFamily(families, "ai_usage_provider_stale")
			gotSeries := 0
			if mf != nil {
				gotSeries = len(mf.Metric)
			}
			if gotSeries != tt.wantSeries {
				t.Fatalf("stale series = %d, want %d", gotSeries, tt.wantSeries)
			}

			for _, reason := range scan.StaleReasons() {
				if tt.wantSeries == 0 {
					continue
				}
				value := gaugeValue(families, "ai_usage_provider_stale", map[string]string{"tool": toolFromResult(tt.result), "reason": reason})
				want := 0.0
				if reason == tt.wantStale {
					want = 1
				}
				if value != want {
					t.Errorf("provider_stale{reason=%q} = %v, want %v", reason, value, want)
				}
			}
			if got := gaugeValue(families, "ai_usage_provider_available", map[string]string{"tool": toolFromResult(tt.result)}); got != tt.wantAvail {
				t.Errorf("provider_available = %v, want %v", got, tt.wantAvail)
			}
		})
	}
}

func TestProviderStaleCardinalityIsFifteenForFiveAvailableTools(t *testing.T) {
	perTool := make(map[string]scan.ToolResult, 5)
	for _, tool := range []string{"alpha", "beta", "gamma", "delta", "epsilon"} {
		perTool[tool] = scan.ToolResult{Available: true}
	}
	c := New(fixedCatalog(), Options{})
	c.Set(scan.Result{PerTool: perTool})
	mf := findFamily(mustGather(t, c), "ai_usage_provider_stale")
	if mf == nil || len(mf.Metric) != 15 {
		got := 0
		if mf != nil {
			got = len(mf.Metric)
		}
		t.Errorf("provider_stale series = %d, want 15", got)
	}
}

func toolFromResult(result scan.Result) string {
	for tool := range result.PerTool {
		return tool
	}
	return ""
}

// TestCollectUnknownModelOmitsCostWithoutCountingAsScanError covers step 8:
// a model absent from the catalog still produces ai_usage_tokens, never
// produces ai_usage_cost_usd for that series, and does not bump
// ai_usage_scan_errors - an unpriced model is new, not broken.
func TestCollectUnknownModelOmitsCostWithoutCountingAsScanError(t *testing.T) {
	c := New(fixedCatalog(), Options{})
	c.Set(fixedResult())

	families := mustGather(t, c)

	if !hasSeries(families, "ai_usage_tokens", map[string]string{"tool": "claude-code", "model": "unknown-model", "token_type": "input", "window": "all"}) {
		t.Error("ai_usage_tokens for the unpriced model is missing")
	}
	if hasSeriesWithLabel(families, "ai_usage_cost_usd", "model", "unknown-model") {
		t.Error("ai_usage_cost_usd exists for an unpriced model, want it absent")
	}

	scanErrors := gaugeValue(families, "ai_usage_scan_errors", map[string]string{"tool": "claude-code"})
	if scanErrors != 1 {
		t.Errorf("ai_usage_scan_errors{tool=claude-code} = %v, want 1 (unchanged by the unpriced model)", scanErrors)
	}
}

func TestScanSuccessIsAlwaysPresentAndPartialStartupPublishesNoUsage(t *testing.T) {
	c := New(fixedCatalog(), Options{})
	families := mustGather(t, c)
	if got := gaugeValue(families, "ai_usage_scan_success", map[string]string{}); got != 0 {
		t.Fatalf("initial ai_usage_scan_success = %v, want 0", got)
	}
	if findFamily(families, "ai_usage_tokens") != nil {
		t.Fatal("usage metrics emitted before a complete scan")
	}
	c.Set(fixedResult())
	families = mustGather(t, c)
	if got := gaugeValue(families, "ai_usage_scan_success", map[string]string{}); got != 1 {
		t.Fatalf("successful ai_usage_scan_success = %v, want 1", got)
	}
	c.MarkScanFailure()
	families = mustGather(t, c)
	if got := gaugeValue(families, "ai_usage_scan_success", map[string]string{}); got != 0 {
		t.Fatalf("failed rescan ai_usage_scan_success = %v, want 0", got)
	}
	if !hasSeries(families, "ai_usage_tokens", map[string]string{"tool": "claude-code", "model": "claude-opus-4", "token_type": "input", "window": "all"}) {
		t.Fatal("failed rescan replaced the prior complete snapshot")
	}
}

// TestCollectCostAlwaysComesFromCatalog covers step 9: model.UsageEvent
// carries no native-cost field, so ai_usage_cost_usd can only ever be
// derived from the pricing catalog - there is nothing else to reconcile
// against, by construction.
func TestCollectCostAlwaysComesFromCatalog(t *testing.T) {
	c := New(fixedCatalog(), Options{})
	c.Set(fixedResult())

	got := gaugeValue(mustGather(t, c), "ai_usage_cost_usd", map[string]string{"tool": "claude-code", "model": "claude-opus-4", "window": "all"})
	want := 100*0.01 + 50*0.02
	if got != want {
		t.Errorf("ai_usage_cost_usd = %v, want %v (catalog rates applied to token counts)", got, want)
	}
}

// TestPromlintClean covers step 10: linting the collector's own exposition
// must report zero problems - naming, units, and missing HELP.
func TestPromlintClean(t *testing.T) {
	c := New(fixedCatalog(), Options{})
	c.Set(fixedResult())

	families := mustGather(t, c)
	problems, err := promlint.NewWithMetricFamilies(families).Lint()
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	for _, p := range problems {
		t.Errorf("promlint: %s: %s", p.Metric, p.Text)
	}
}
