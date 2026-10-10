// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

// Package dashboard has no production code of its own: it exists to pin
// deploy/grafana/dashboards/ai-usage-overview.json against the metric
// contract in docs/metrics.md, so a panel referencing a metric name that
// doesn't exist (typo or a metric that got renamed) breaks CI instead of
// shipping a dashboard with a broken query.
package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	dashboards "github.com/danielrigobertojs/ai-usage-exporter/deploy/grafana/dashboards"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/aggregate"
)

type panel struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Targets     []target `json:"targets"`
	Panels      []panel  `json:"panels"` // defensive: collapsed Grafana rows nest panels here
}

type target struct {
	Expr string `json:"expr"`
}

type grafanaDashboard struct {
	Title      string              `json:"title"`
	Refresh    string              `json:"refresh"`
	Templating dashboardTemplating `json:"templating"`
	Panels     []panel             `json:"panels"`
}

type dashboardTemplating struct {
	List []dashboardVariable `json:"list"`
}

type dashboardVariable struct {
	Name    string                    `json:"name"`
	Options []dashboardVariableOption `json:"options"`
}

type dashboardVariableOption struct {
	Value string `json:"value"`
}

var wantPanelTitles = []string{
	"Now: tokens (1h)",
	"Now: cost (1h)",
	"Now: live usage (1h)",
	"Now: scan freshness",
	"Status row",
	"Tokens by tool",
	"Cost by model",
	"Token-class distribution",
	"Token trend",
	"Month-to-date cost",
	"Exporter health",
}

func flattenPanels(panels []panel) []panel {
	var out []panel
	for _, p := range panels {
		out = append(out, p)
		out = append(out, flattenPanels(p.Panels)...)
	}
	return out
}

func loadDashboard(t *testing.T) grafanaDashboard {
	t.Helper()
	var d grafanaDashboard
	if err := json.Unmarshal(dashboards.AIUsageOverviewJSON, &d); err != nil {
		t.Fatalf("ai-usage-overview.json does not deserialize: %v", err)
	}
	return d
}

func TestDashboardDeserializes(t *testing.T) {
	d := loadDashboard(t)
	if len(d.Panels) == 0 {
		t.Fatal("dashboard deserialized but has zero panels")
	}
}

func TestDashboardRefreshAndWindowOptions(t *testing.T) {
	d := loadDashboard(t)
	if d.Refresh != "30s" {
		t.Errorf("dashboard refresh = %q, want 30s for live monitoring", d.Refresh)
	}

	var window dashboardVariable
	for _, variable := range d.Templating.List {
		if variable.Name == "window" {
			window = variable
			break
		}
	}
	if window.Name == "" {
		t.Fatal("dashboard has no window variable")
	}

	got := make([]string, 0, len(window.Options))
	for _, option := range window.Options {
		got = append(got, option.Value)
	}
	want := []string{
		string(aggregate.Window1h),
		string(aggregate.Window24h),
		string(aggregate.Window7d),
		string(aggregate.Window30d),
		string(aggregate.WindowMTD),
		string(aggregate.WindowAll),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("window options = %v, want %v", got, want)
	}
}

func TestDashboardHasExpectedPanels(t *testing.T) {
	d := loadDashboard(t)
	flat := flattenPanels(d.Panels)

	got := make([]string, 0, len(flat))
	for _, p := range flat {
		got = append(got, p.Title)
	}
	sort.Strings(got)

	want := append([]string(nil), wantPanelTitles...)
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("expected exactly %d panels, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("panel titles mismatch: got %v, want %v", got, want)
		}
	}
}

func TestLivePanelsRepresentEmptyUsageAsZero(t *testing.T) {
	d := loadDashboard(t)
	want := map[string]string{
		"Now: tokens (1h)":     "sum(ai_usage_tokens{window=\"1h\"}) or vector(0)",
		"Now: cost (1h)":       "sum(ai_usage_cost_usd{window=\"1h\"}) or vector(0)",
		"Now: live usage (1h)": "sum by (tool) (ai_usage_tokens{window=\"1h\"}) or (0 * max by (tool) (ai_usage_provider_available))",
	}
	for _, p := range flattenPanels(d.Panels) {
		expr, ok := want[p.Title]
		if !ok {
			continue
		}
		if len(p.Targets) != 1 || p.Targets[0].Expr != expr {
			got := ""
			if len(p.Targets) == 1 {
				got = p.Targets[0].Expr
			}
			t.Errorf("panel %q expression = %q, want %q", p.Title, got, expr)
		}
		delete(want, p.Title)
	}
	for title := range want {
		t.Errorf("dashboard is missing live panel %q", title)
	}
}

// contractMetricHeader matches a metrics.md section header for one metric,
// e.g. "### `ai_usage_tokens`".
var contractMetricHeader = regexp.MustCompile("^### `(ai_usage_[a-z0-9_]+)`$")

// exprMetricName matches any ai_usage_* identifier referenced inside a
// PromQL expression.
var exprMetricName = regexp.MustCompile(`ai_usage_[a-zA-Z0-9_]*`)

func contractedMetricNames(t *testing.T) map[string]bool {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not resolve path of dashboard_test.go")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	metricsDoc := filepath.Join(repoRoot, "docs", "metrics.md")

	raw, err := os.ReadFile(metricsDoc)
	if err != nil {
		t.Fatalf("reading %s: %v", metricsDoc, err)
	}

	names := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		if m := contractMetricHeader.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			names[m[1]] = true
		}
	}

	// A format change in docs/metrics.md (different heading level, renamed
	// section) must fail loudly here, not silently validate every panel
	// against an empty allow-list. check-spdx.sh had exactly this failure
	// mode once: green because it examined zero files.
	if len(names) == 0 {
		t.Fatalf("extracted zero metric names from %s; the heading format probably changed and this test needs updating", metricsDoc)
	}

	return names
}

func allExprs(t *testing.T) []string {
	d := loadDashboard(t)
	var exprs []string
	for _, p := range flattenPanels(d.Panels) {
		for _, tg := range p.Targets {
			if tg.Expr != "" {
				exprs = append(exprs, tg.Expr)
			}
		}
	}
	if len(exprs) == 0 {
		t.Fatal("dashboard has zero panel expressions; nothing to validate")
	}
	return exprs
}

func TestDashboardExpressionsOnlyReferenceContractedMetrics(t *testing.T) {
	contracted := contractedMetricNames(t)

	for _, expr := range allExprs(t) {
		for _, name := range exprMetricName.FindAllString(expr, -1) {
			if !contracted[name] {
				t.Errorf("expr %q references metric %q, which is not documented in docs/metrics.md", expr, name)
			}
		}
	}
}

func TestDashboardExpressionsNeverUseRateOrIncrease(t *testing.T) {
	for _, expr := range allExprs(t) {
		if strings.Contains(expr, "rate(") || strings.Contains(expr, "increase(") {
			t.Errorf("expr %q uses rate()/increase() over a window gauge (ADR-001) - this is always wrong for these series", expr)
		}
	}
}

func TestDashboardPanelsHaveNonEmptyDescriptions(t *testing.T) {
	d := loadDashboard(t)
	for _, p := range flattenPanels(d.Panels) {
		if strings.TrimSpace(p.Description) == "" {
			t.Errorf("panel %q has an empty description", p.Title)
		}
	}
}
