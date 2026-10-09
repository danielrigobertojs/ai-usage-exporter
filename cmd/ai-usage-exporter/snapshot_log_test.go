// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/aggregate"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/pricing"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/scan"
)

// captureJSONLogs installs a JSON slog handler over a buffer for the
// duration of the test and restores the process default afterwards. It is
// the only way to observe what logSnapshotPublished actually emits: that
// line is the terminal event of a scan and, with re-scans every 60 s, the
// one an operator correlates by scan_id.
func captureJSONLogs(t *testing.T, level slog.Level) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	old := slog.Default()
	t.Cleanup(func() { slog.SetDefault(old) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: level})))
	return buf
}

// lastRecord returns the last log line whose msg matches, decoded.
func lastRecord(t *testing.T, buf *bytes.Buffer, msg string) map[string]any {
	t.Helper()
	var found map[string]any
	for _, line := range bytes.Split(buf.Bytes(), []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("log line is not JSON (%v): %s", err, line)
		}
		if rec["msg"] == msg {
			found = rec
		}
	}
	if found == nil {
		t.Fatalf("no %q line in %s", msg, buf.String())
	}
	return found
}

// snapshotFixture builds a scan.Result with exactly wantEvents distinct
// events for one tool and model, through the real aggregator rather than a
// hand-built Snapshot: the assertions below exist to prove `events` is
// derived from what was aggregated, so the fixture must not hand it the
// answer.
func snapshotFixture(t *testing.T, wantEvents int, now time.Time) scan.Result {
	t.Helper()
	agg := aggregate.New(now, time.UTC)
	for i := 0; i < wantEvents; i++ {
		added := agg.Add(model.UsageEvent{
			Key:       model.EventKey{Tool: "claude-code", SessionID: "s1", MessageID: fmt.Sprintf("m%d", i)},
			Tool:      "claude-code",
			Model:     "claude-opus-5",
			Role:      "assistant",
			Timestamp: now,
			Tokens:    map[model.TokenClass]int64{model.TokenInput: 1000, model.TokenOutput: 500, model.TokenCacheRead: 2000},
		})
		if !added {
			t.Fatalf("event %d was rejected or deduplicated", i)
		}
	}
	return scan.Result{
		Snapshot: agg.Snapshot(),
		// One provider, deliberately different from wantEvents: the defect
		// this file exists to prevent reported len(PerTool) here.
		PerTool: map[string]scan.ToolResult{"claude-code": {Available: true, FilesScanned: 1}},
	}
}

// TestLogSnapshotPublishedCarriesScanID pins the correlation id on the
// terminal event of a scan. scan.Run attaches it with scan.WithID, but
// snapshot published is emitted from cmd/, outside Run, so nothing else in
// the codebase would notice its loss.
func TestLogSnapshotPublishedCarriesScanID(t *testing.T) {
	const scanID = "abc123def456"
	buf := captureJSONLogs(t, slog.LevelInfo)
	logSnapshotPublished(scanID, snapshotFixture(t, 3, time.Now()), pricing.Embedded())

	if got := lastRecord(t, buf, "snapshot published")["scan_id"]; got != scanID {
		t.Errorf("scan_id = %v, want %q", got, scanID)
	}
}

// TestLogSnapshotPublishedCountsAggregatedEvents pins events to the number
// of events the aggregator accepted. It used to be len(PerTool), which
// reported "events=3" after ingesting 324 files: three providers, not three
// events.
func TestLogSnapshotPublishedCountsAggregatedEvents(t *testing.T) {
	buf := captureJSONLogs(t, slog.LevelInfo)
	logSnapshotPublished("scan-1", snapshotFixture(t, 7, time.Now()), pricing.Embedded())

	if got := lastRecord(t, buf, "snapshot published")["events"]; got != float64(7) {
		t.Errorf("events = %v, want 7 (aggregated events, not the provider count)", got)
	}
}

// TestLogSnapshotPublishedReportsCatalogCost pins cost_usd to the
// window="all" token counts priced with the catalog in effect at scan time.
// The expectation is expressed through catalog.Lookup rather than a
// literal: what is at stake is that the line prices all-history once, not
// which rate table happens to be compiled in.
func TestLogSnapshotPublishedReportsCatalogCost(t *testing.T) {
	const (
		events    = 3
		inputToks = 1000
		outToks   = 500
		cacheToks = 2000
	)
	catalog := pricing.Embedded()
	rates, ok := catalog.Lookup("claude-code", "claude-opus-5")
	if !ok {
		t.Skip("claude-opus-5 is not in the embedded catalog; there is nothing to price")
	}
	// Rates are already per token: decodeCatalogFile folds the per-million
	// prices from models.dev into per-token ones.
	want := float64(events) * (inputToks*rates.Input + outToks*rates.Output + cacheToks*rates.CacheRead)

	buf := captureJSONLogs(t, slog.LevelInfo)
	logSnapshotPublished("scan-1", snapshotFixture(t, events, time.Now()), catalog)

	got, ok := lastRecord(t, buf, "snapshot published")["cost_usd"].(float64)
	if !ok || got <= 0 {
		t.Fatalf("cost_usd = %v, want a positive number", got)
	}
	if diff := got - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("cost_usd = %v, want %v (all-history window priced exactly once)", got, want)
	}
}

// TestLogSnapshotPublishedIsCorrelatableWithScanComplete is the end-to-end
// shape of the contract: the publication line has to name the same scan as
// the completion line, which is what makes a failed re-scan traceable.
func TestLogSnapshotPublishedIsCorrelatableWithScanComplete(t *testing.T) {
	const scanID = "0f68a47195ad"
	result := snapshotFixture(t, 2, time.Now())

	buf := captureJSONLogs(t, slog.LevelDebug)
	slog.Default().With("scan_id", scanID).Info("scan complete", "duration", time.Millisecond, "tools", len(result.PerTool))
	logSnapshotPublished(scanID, result, pricing.Embedded())

	complete := lastRecord(t, buf, "scan complete")["scan_id"]
	published := lastRecord(t, buf, "snapshot published")["scan_id"]
	if complete != published {
		t.Errorf("scan complete scan_id = %v, snapshot published scan_id = %v; they must match", complete, published)
	}
}
