// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval
package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/aggregate"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/collector"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/config"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/pricing"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/scan"
)

func TestIsCLICommandKeepsFlagsOnServePath(t *testing.T) {
	for _, tc := range []struct {
		arg  string
		want bool
	}{
		{"report", true}, {"doctor", true}, {"serve", false}, {"--listen", false}, {"--scan-timeout=1s", false},
	} {
		if got := isCLICommand(tc.arg); got != tc.want {
			t.Errorf("isCLICommand(%q)=%t, want %t", tc.arg, got, tc.want)
		}
	}
}

func summaryResult() scan.Result {
	return scan.Result{Snapshot: aggregate.Snapshot{
		Events: 7,
		Tokens: map[aggregate.TokenKey]int64{
			{Tool: "test", Model: "model", Class: model.TokenInput, Window: aggregate.WindowAll}:  10,
			{Tool: "test", Model: "model", Class: model.TokenOutput, Window: aggregate.WindowAll}: 5,
			{Tool: "test", Model: "model", Class: model.TokenInput, Window: aggregate.Window24h}:  10,
		},
	}}
}

func TestSnapshotSummaryUsesOnlyAllWindow(t *testing.T) {
	r := summaryResult()
	if got := tokenCount(r); got != 15 {
		t.Errorf("tokenCount = %d, want 15 from window=all only", got)
	}
	if got := seriesCount(r); got != 2 {
		t.Errorf("seriesCount = %d, want 2 from window=all only", got)
	}
	if got := eventCount(r); got != 7 {
		t.Errorf("eventCount = %d, want 7", got)
	}
}

func TestLogSnapshotPublishedCarriesCorrelatedSummary(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	t.Cleanup(func() { slog.SetDefault(old) })
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))

	logSnapshotPublished("scan-under-test", summaryResult(), pricing.Embedded())
	line := logs.String()
	for _, want := range []string{"snapshot published", "scan_id=scan-under-test", "events=7", "tokens=15", "series=2", "cost_usd="} {
		if !strings.Contains(line, want) {
			t.Errorf("snapshot log missing %q: %s", want, line)
		}
	}
}

func TestStartupScanFailurePolicy(t *testing.T) {
	c := collector.New(pricing.Embedded(), collector.Options{})
	cfg := config.Config{}
	if err := applyStartupScanResult(cfg, c, scan.Result{}, context.DeadlineExceeded, pricing.Embedded(), "scan-under-test"); err != nil {
		t.Fatalf("degraded startup returned error: %v", err)
	}
	if c.Ready() {
		t.Fatal("degraded startup published a partial usage snapshot")
	}

	cfg.FailOnStartupScanError = true
	err := applyStartupScanResult(cfg, collector.New(pricing.Embedded(), collector.Options{}), scan.Result{}, context.DeadlineExceeded, pricing.Embedded(), "scan-under-test")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("strict startup error = %v, want context deadline exceeded", err)
	}
}

func TestSuccessfulStartupPublishesSnapshot(t *testing.T) {
	c := collector.New(pricing.Embedded(), collector.Options{})
	if err := applyStartupScanResult(config.Config{}, c, summaryResult(), nil, pricing.Embedded(), "scan-under-test"); err != nil {
		t.Fatal(err)
	}
	if !c.Ready() {
		t.Fatal("successful startup did not publish its snapshot")
	}
}
