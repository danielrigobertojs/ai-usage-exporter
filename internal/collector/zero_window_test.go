// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package collector

import (
	"testing"
	"time"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/aggregate"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/scan"
)

func scopeMetric(familiesName string, families map[string]float64, tool string, window aggregate.Window) (float64, bool) {
	v, ok := families[familiesName+"/"+tool+"/"+string(window)]
	return v, ok
}

func collectScopes(t *testing.T, c *Collector, metric string) map[string]float64 {
	t.Helper()
	values := map[string]float64{}
	for _, family := range mustGather(t, c) {
		if family.GetName() != metric {
			continue
		}
		for _, sample := range family.Metric {
			labels := map[string]string{}
			for _, label := range sample.Label {
				labels[label.GetName()] = label.GetValue()
			}
			values[metric+"/"+labels["tool"]+"/"+labels["window"]] = sample.GetGauge().GetValue()
		}
	}
	return values
}

func TestCollectMaterializesClosedScopeZeros(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	c := New(fixedCatalog(), Options{})
	c.Set(scan.Result{
		Snapshot: aggregate.Snapshot{
			ScannedAt: now,
			Tokens: map[aggregate.TokenKey]int64{
				{Tool: "claude-code", Model: "claude-opus-5", Class: model.TokenInput, Window: aggregate.Window24h}: 10,
			},
			Sessions:    map[aggregate.ScopeKey]int64{{Tool: "claude-code", Window: aggregate.Window24h}: 1},
			ToolCalls:   map[aggregate.ScopeKey]int64{{Tool: "claude-code", Window: aggregate.Window24h}: 2},
			LastEventAt: map[string]time.Time{},
		},
		PerTool: map[string]scan.ToolResult{
			"claude-code": {Available: true},
			"codex":       {Available: true},
			"opencode":    {Available: false},
		},
	})

	for _, metric := range []string{"ai_usage_sessions", "ai_usage_tool_calls"} {
		values := collectScopes(t, c, metric)
		tools := []string{"claude-code", "codex"}
		if metric == "ai_usage_tool_calls" {
			tools = []string{"claude-code"}
		}
		for _, tool := range tools {
			for _, window := range aggregate.AllWindows() {
				value, ok := scopeMetric(metric, values, tool, window)
				if !ok {
					t.Errorf("%s{tool=%q,window=%q} is absent; want a zero-capable closed scope", metric, tool, window)
					continue
				}
				if tool == "claude-code" && window == aggregate.Window24h {
					continue
				}
				if value != 0 {
					t.Errorf("%s{tool=%q,window=%q} = %v, want 0", metric, tool, window, value)
				}
			}
		}
		for _, window := range aggregate.AllWindows() {
			if metric == "ai_usage_sessions" {
				if _, ok := scopeMetric(metric, values, "opencode", window); ok {
					t.Errorf("%s{tool=opencode,window=%q} exists for unavailable provider", metric, window)
				}
			}
			if metric == "ai_usage_tool_calls" {
				if _, ok := scopeMetric(metric, values, "codex", window); ok {
					t.Errorf("%s{tool=codex,window=%q} exists although codex did not report tool calls", metric, window)
				}
			}
		}
	}

	families := mustGather(t, c)
	if hasSeries(families, "ai_usage_tokens", map[string]string{"tool": "codex", "model": "claude-opus-5", "token_type": "input", "window": "1h"}) {
		t.Error("zero-filling must not invent token series for an open model label")
	}
	if hasSeries(families, "ai_usage_cost_usd", map[string]string{"tool": "codex", "model": "claude-opus-5", "window": "1h"}) {
		t.Error("zero-filling must not invent cost series for an open model label")
	}
}

func TestCollectDoesNotInventToolCallsZeroForToolsThatNeverReportThem(t *testing.T) {
	c := New(fixedCatalog(), Options{})
	c.Set(scan.Result{
		Snapshot: aggregate.Snapshot{
			ScannedAt: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC),
			Tokens: map[aggregate.TokenKey]int64{
				{Tool: "claude-code", Model: "claude-opus-4", Class: model.TokenInput, Window: aggregate.WindowAll}: 10,
				{Tool: "opencode", Model: "opencode-model", Class: model.TokenInput, Window: aggregate.WindowAll}:   20,
			},
			Sessions: map[aggregate.ScopeKey]int64{
				{Tool: "claude-code", Window: aggregate.WindowAll}: 2,
				{Tool: "opencode", Window: aggregate.WindowAll}:    3,
			},
			ToolCalls: map[aggregate.ScopeKey]int64{{Tool: "claude-code", Window: aggregate.WindowAll}: 9},
		},
		PerTool: map[string]scan.ToolResult{
			"claude-code": {Available: true},
			"opencode":    {Available: true},
		},
	})

	families := mustGather(t, c)
	if got := gaugeValue(families, "ai_usage_tool_calls", map[string]string{"tool": "claude-code", "window": "1h"}); got != 0 {
		t.Errorf("ai_usage_tool_calls{tool=claude-code,window=1h} = %v, want 0", got)
	}
	for _, window := range aggregate.AllWindows() {
		if hasSeries(families, "ai_usage_tool_calls", map[string]string{"tool": "opencode", "window": string(window)}) {
			t.Errorf("ai_usage_tool_calls{tool=opencode,window=%q} exists although opencode did not report tool calls", window)
		}
	}
}
