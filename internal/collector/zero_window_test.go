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
		for _, tool := range []string{"claude-code", "codex"} {
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
			if _, ok := scopeMetric(metric, values, "opencode", window); ok {
				t.Errorf("%s{tool=opencode,window=%q} exists for unavailable provider", metric, window)
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
