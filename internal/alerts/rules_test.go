// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

// Package alerts pins shipped Prometheus alert rules to the public metrics
// contract. It intentionally has no production Go code.
package alerts

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type ruleFile struct {
	Groups []ruleGroup `yaml:"groups"`
}

type ruleGroup struct {
	Name  string      `yaml:"name"`
	Rules []alertRule `yaml:"rules"`
}

type alertRule struct {
	Alert string `yaml:"alert"`
	Expr  string `yaml:"expr"`
	For   string `yaml:"for"`
}

type ruleTestFile struct {
	RuleFiles []string `yaml:"rule_files"`
}

var metricName = regexp.MustCompile(`ai_usage_[a-zA-Z0-9_]*`)

func loadRules(t *testing.T) []alertRule {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not resolve path of rules_test.go")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	raw, err := os.ReadFile(filepath.Join(repoRoot, "deploy", "prometheus", "ai-usage-exporter.rules.yml"))
	if err != nil {
		t.Fatalf("reading alert rules: %v", err)
	}

	var parsed ruleFile
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parsing alert rules: %v", err)
	}
	if len(parsed.Groups) != 1 || parsed.Groups[0].Name != "ai-usage-exporter" {
		t.Fatalf("rule groups = %#v, want one ai-usage-exporter group", parsed.Groups)
	}
	return parsed.Groups[0].Rules
}

func TestPrometheusConfigurationLoadsAlertRules(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not resolve path of rules_test.go")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	raw, err := os.ReadFile(filepath.Join(repoRoot, "deploy", "prometheus", "prometheus.yml"))
	if err != nil {
		t.Fatalf("reading Prometheus configuration: %v", err)
	}
	var config struct {
		RuleFiles []string `yaml:"rule_files"`
	}
	if err := yaml.Unmarshal(raw, &config); err != nil {
		t.Fatalf("parsing Prometheus configuration: %v", err)
	}
	if len(config.RuleFiles) != 1 || config.RuleFiles[0] != "ai-usage-exporter.rules.yml" {
		t.Errorf("rule_files = %v, want [ai-usage-exporter.rules.yml]", config.RuleFiles)
	}
}

func TestRuleTestFixtureLoadsShippedRules(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not resolve path of rules_test.go")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	fixturePath := filepath.Join(repoRoot, "deploy", "prometheus", "testdata", "ai-usage-exporter.rules.test.yml")
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("reading Prometheus rule test fixture: %v", err)
	}

	var fixture ruleTestFile
	if err := yaml.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("parsing Prometheus rule test fixture: %v", err)
	}
	if len(fixture.RuleFiles) != 1 || fixture.RuleFiles[0] != "../ai-usage-exporter.rules.yml" {
		t.Fatalf("fixture rule_files = %v, want [../ai-usage-exporter.rules.yml]", fixture.RuleFiles)
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(fixturePath), fixture.RuleFiles[0]))
	if resolved != filepath.Join(repoRoot, "deploy", "prometheus", "ai-usage-exporter.rules.yml") {
		t.Fatalf("fixture rule file resolves to %s, want shipped rule file", resolved)
	}
	if _, err := os.Stat(resolved); err != nil {
		t.Fatalf("fixture rule file %s is not readable: %v", resolved, err)
	}
}

func contractedMetricNames(t *testing.T) map[string]bool {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not resolve path of rules_test.go")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	raw, err := os.ReadFile(filepath.Join(repoRoot, "docs", "metrics.md"))
	if err != nil {
		t.Fatalf("reading metrics contract: %v", err)
	}

	header := regexp.MustCompile("^### `(" + metricName.String() + ")`$")
	names := make(map[string]bool)
	for _, line := range strings.Split(string(raw), "\n") {
		if matches := header.FindStringSubmatch(strings.TrimSpace(line)); matches != nil {
			names[matches[1]] = true
		}
	}
	if len(names) == 0 {
		t.Fatal("extracted zero metric names from docs/metrics.md")
	}
	return names
}

func TestAlertRulesMatchMetricContractAndGaugeSemantics(t *testing.T) {
	contracted := contractedMetricNames(t)
	for _, rule := range loadRules(t) {
		if rule.Alert == "" || rule.Expr == "" || rule.For == "" {
			t.Errorf("rule %#v must define alert, expr, and for", rule)
		}
		for _, name := range metricName.FindAllString(rule.Expr, -1) {
			if !contracted[name] {
				t.Errorf("%s references metric %q not in docs/metrics.md", rule.Alert, name)
			}
		}
		lower := strings.ToLower(rule.Expr)
		if strings.Contains(lower, "rate(") || strings.Contains(lower, "increase(") {
			t.Errorf("%s uses rate()/increase() over a gauge: %q", rule.Alert, rule.Expr)
		}
	}
}

func TestAlertRulesCoverExpectedFailureSignals(t *testing.T) {
	want := map[string]struct {
		expr string
		for_ string
	}{
		"AIUsageExporterDown":          {`up{job=~"ai-usage-exporter|ai-worker"} == 0`, "5m"},
		"AIUsageScanFailed":            {"ai_usage_scan_success == 0", "5m"},
		"AIUsageProviderUnavailable":   {"ai_usage_provider_available == 0 and on (instance, tool) max_over_time(ai_usage_provider_available[24h]) == 1", "10m"},
		"AIUsageProviderNoUsageEvents": {`max by (instance, tool) (ai_usage_provider_stale{reason="no_usage_events"}) == 1`, "10m"},
		"AIUsageScanStale":             {"time() - ai_usage_scan_timestamp_seconds > 300", "5m"},
	}

	for _, rule := range loadRules(t) {
		expectation, ok := want[rule.Alert]
		if !ok {
			t.Errorf("unexpected alert %q", rule.Alert)
			continue
		}
		if rule.Expr != expectation.expr || rule.For != expectation.for_ {
			t.Errorf("%s = expr %q for %q, want expr %q for %q", rule.Alert, rule.Expr, rule.For, expectation.expr, expectation.for_)
		}
		delete(want, rule.Alert)
	}
	for alert := range want {
		t.Errorf("missing alert %q", alert)
	}
}

func TestProviderUnavailableRequiresPriorAvailability(t *testing.T) {
	for _, rule := range loadRules(t) {
		if rule.Alert != "AIUsageProviderUnavailable" {
			continue
		}
		want := "ai_usage_provider_available == 0 and on (instance, tool) max_over_time(ai_usage_provider_available[24h]) == 1"
		if rule.Expr != want {
			t.Errorf("AIUsageProviderUnavailable expression = %q, want %q", rule.Expr, want)
		}
		return
	}
	t.Fatal("AIUsageProviderUnavailable rule is missing")
}

func TestNoUsageEventsAlertExcludesIdleReason(t *testing.T) {
	for _, rule := range loadRules(t) {
		if rule.Alert != "AIUsageProviderNoUsageEvents" {
			continue
		}
		if !strings.Contains(rule.Expr, `reason="no_usage_events"`) || strings.Contains(rule.Expr, "last_event_too_old") {
			t.Errorf("ADR-010 alert must select no_usage_events and exclude idle providers: %q", rule.Expr)
		}
		return
	}
	t.Fatal("AIUsageProviderNoUsageEvents rule is missing")
}
