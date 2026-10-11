// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

// Package dashboard has no production code of its own: it exists to pin the
// shipped Grafana dashboards against the metric contract in docs/metrics.md,
// so a panel referencing a metric name that
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
	Name       string                    `json:"name"`
	Type       string                    `json:"type"`
	Query      string                    `json:"query"`
	Multi      bool                      `json:"multi"`
	IncludeAll bool                      `json:"includeAll"`
	AllValue   string                    `json:"allValue"`
	Options    []dashboardVariableOption `json:"options"`
}

type dashboardVariableOption struct {
	Value string `json:"value"`
}

type dashboardFixture struct {
	name            string
	raw             []byte
	wantRefresh     string
	wantPanelTitles []string
}

var dashboardFixtures = []dashboardFixture{
	{
		name:        "ai-usage-overview.json",
		raw:         dashboards.AIUsageOverviewJSON,
		wantRefresh: "30s",
		wantPanelTitles: []string{
			"Now: tokens (1h)", "Now: cost (1h)", "Now: live usage (1h)",
			"Now: scan freshness", "Status row", "Tokens by tool", "Cost by model",
			"Token-class distribution", "Token trend", "Month-to-date cost", "Exporter health",
		},
	},
	{
		name:        "ai-usage-live.json",
		raw:         dashboards.AIUsageLiveJSON,
		wantRefresh: "10s",
		wantPanelTitles: []string{
			"Ahora: tokens (1h)", "Ahora: coste (1h)", "Instancias reportando",
			"Frescura maxima del escaneo", "Tokens 1h por instancia", "Tokens 1h por provider",
			"Tokens 1h por modelo (top 8)", "Clases de token por instancia (1h)",
			"Coste 1h por instancia", "Coste por modelo (ventana $window)",
			"Detalle por modelo (ventana $window)", "Salud por instancia y provider",
		},
	},
}

func flattenPanels(panels []panel) []panel {
	var out []panel
	for _, p := range panels {
		out = append(out, p)
		out = append(out, flattenPanels(p.Panels)...)
	}
	return out
}

func loadDashboard(t *testing.T, fixture dashboardFixture) grafanaDashboard {
	t.Helper()
	var d grafanaDashboard
	if err := json.Unmarshal(fixture.raw, &d); err != nil {
		t.Fatalf("%s does not deserialize: %v", fixture.name, err)
	}
	return d
}

func findVariable(d grafanaDashboard, name string) dashboardVariable {
	for _, variable := range d.Templating.List {
		if variable.Name == name {
			return variable
		}
	}
	return dashboardVariable{}
}

func TestDashboardDeserializes(t *testing.T) {
	for _, fixture := range dashboardFixtures {
		t.Run(fixture.name, func(t *testing.T) {
			d := loadDashboard(t, fixture)
			if len(d.Panels) == 0 {
				t.Fatal("dashboard deserialized but has zero panels")
			}
		})
	}
}

func TestDashboardRefreshAndWindowOptions(t *testing.T) {
	for _, fixture := range dashboardFixtures {
		t.Run(fixture.name, func(t *testing.T) {
			d := loadDashboard(t, fixture)
			if d.Refresh != fixture.wantRefresh {
				t.Errorf("dashboard refresh = %q, want %s", d.Refresh, fixture.wantRefresh)
			}

			window := findVariable(d, "window")
			if window.Name == "" {
				t.Fatal("dashboard has no window variable")
			}

			got := make([]string, 0, len(window.Options))
			for _, option := range window.Options {
				got = append(got, option.Value)
			}
			want := []string{string(aggregate.Window1h), string(aggregate.Window24h), string(aggregate.Window7d), string(aggregate.Window30d), string(aggregate.WindowMTD), string(aggregate.WindowAll)}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("window options = %v, want %v", got, want)
			}
		})
	}
}

func TestDashboardHasExpectedPanels(t *testing.T) {
	for _, fixture := range dashboardFixtures {
		t.Run(fixture.name, func(t *testing.T) {
			d := loadDashboard(t, fixture)
			flat := flattenPanels(d.Panels)
			got := make([]string, 0, len(flat))
			for _, p := range flat {
				got = append(got, p.Title)
			}
			sort.Strings(got)
			want := append([]string(nil), fixture.wantPanelTitles...)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("panel titles mismatch: got %v, want %v", got, want)
			}
		})
	}
}

func TestLivePanelsRepresentEmptyUsageAsZero(t *testing.T) {
	d := loadDashboard(t, dashboardFixtures[0])
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

type dashboardExpr struct {
	dashboard string
	expr      string
}

func allExprs(t *testing.T) []dashboardExpr {
	var exprs []dashboardExpr
	for _, fixture := range dashboardFixtures {
		d := loadDashboard(t, fixture)
		for _, p := range flattenPanels(d.Panels) {
			for _, tg := range p.Targets {
				if tg.Expr != "" {
					exprs = append(exprs, dashboardExpr{dashboard: fixture.name, expr: tg.Expr})
				}
			}
		}
	}
	if len(exprs) == 0 {
		t.Fatal("dashboards have zero panel expressions; nothing to validate")
	}
	return exprs
}

func TestDashboardExpressionsOnlyReferenceContractedMetrics(t *testing.T) {
	contracted := contractedMetricNames(t)

	for _, item := range allExprs(t) {
		for _, name := range exprMetricName.FindAllString(item.expr, -1) {
			if !contracted[name] {
				t.Errorf("%s expr %q references metric %q, which is not documented in docs/metrics.md", item.dashboard, item.expr, name)
			}
		}
	}
}

func TestDashboardExpressionsNeverUseRateOrIncrease(t *testing.T) {
	for _, item := range allExprs(t) {
		if strings.Contains(item.expr, "rate(") || strings.Contains(item.expr, "increase(") {
			t.Errorf("%s expr %q uses rate()/increase() over a window gauge (ADR-001) - this is always wrong for these series", item.dashboard, item.expr)
		}
	}
}

func TestDashboardPanelsHaveNonEmptyDescriptions(t *testing.T) {
	for _, fixture := range dashboardFixtures {
		d := loadDashboard(t, fixture)
		for _, p := range flattenPanels(d.Panels) {
			if strings.TrimSpace(p.Description) == "" {
				t.Errorf("%s panel %q has an empty description", fixture.name, p.Title)
			}
		}
	}
}

func TestLiveDashboardVariables(t *testing.T) {
	d := loadDashboard(t, dashboardFixtures[1])
	want := map[string]dashboardVariable{
		"datasource": {Name: "datasource", Type: "datasource", Query: "prometheus"},
		"instance":   {Name: "instance", Type: "query", Query: "label_values(ai_usage_tokens, instance)", Multi: true, IncludeAll: true, AllValue: ".*"},
		"tool":       {Name: "tool", Type: "query", Query: "label_values(ai_usage_tokens{instance=~\"$instance\"}, tool)", Multi: true, IncludeAll: true, AllValue: ".*"},
		"model":      {Name: "model", Type: "query", Query: "label_values(ai_usage_tokens{instance=~\"$instance\", tool=~\"$tool\"}, model)", Multi: true, IncludeAll: true, AllValue: ".*"},
		"window":     {Name: "window", Type: "custom", Query: "1h,24h,7d,30d,mtd,all"},
	}
	if len(d.Templating.List) != len(want) {
		t.Fatalf("live dashboard has %d variables, want %d", len(d.Templating.List), len(want))
	}
	for name, expected := range want {
		got := findVariable(d, name)
		if got.Name == "" {
			t.Errorf("live dashboard is missing %q variable", name)
			continue
		}
		if got.Name != expected.Name || got.Type != expected.Type || got.Query != expected.Query || got.Multi != expected.Multi || got.IncludeAll != expected.IncludeAll || got.AllValue != expected.AllValue {
			t.Errorf("live variable %q = %#v, want %#v", name, got, expected)
		}
	}
}

var aggregationLabels = regexp.MustCompile(`(?:sum|count|max|min|avg) by \(([^)]*)\)`)

func TestLiveDashboardAggregationsUseSelectionVariables(t *testing.T) {
	d := loadDashboard(t, dashboardFixtures[1])
	selectors := map[string]string{"instance": `instance=~"$instance"`, "tool": `tool=~"$tool"`, "model": `model=~"$model"`}
	for _, p := range flattenPanels(d.Panels) {
		for _, target := range p.Targets {
			for _, match := range aggregationLabels.FindAllStringSubmatch(target.Expr, -1) {
				for label, selector := range selectors {
					if strings.Contains(match[1], label) && !strings.Contains(target.Expr, selector) {
						t.Errorf("panel %q aggregates by %q but expr %q lacks selector %s", p.Title, label, target.Expr, selector)
					}
				}
			}
		}
	}
}
