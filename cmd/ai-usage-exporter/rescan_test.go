// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/collector"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/config"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/pricing"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider"
)

// countingProvider increments count on every Parse call and, if block is
// non-nil, waits for it to be closed before returning - used to hold a scan
// "in flight" long enough for a test to observe trigger-coalescing.
type countingProvider struct {
	count   *int32
	block   <-chan struct{}
	started chan<- struct{}
}

func (p countingProvider) Descriptor() provider.Descriptor {
	return provider.Descriptor{
		ID:   "counter",
		Kind: provider.SourceJSONL,
		Roots: []provider.RootSpec{
			{Base: provider.BaseHome, Rel: ".counter", Glob: "*.jsonl"},
		},
	}
}

func (p countingProvider) Parse(ctx context.Context, src provider.Source, emit func(model.UsageEvent) error) error {
	if p.started != nil {
		p.started <- struct{}{}
	}
	if p.block != nil {
		<-p.block
	}
	atomic.AddInt32(p.count, 1)
	return emit(model.UsageEvent{
		Key:       model.EventKey{Tool: "counter", SessionID: src.Path, MessageID: fmt.Sprintf("%s#%d", src.Path, atomic.LoadInt32(p.count))},
		Tool:      "counter",
		Model:     "counter-model",
		Timestamp: time.Now().UTC(),
		Tokens:    map[model.TokenClass]int64{model.TokenInput: 1},
	})
}

func counterEnv() provider.Env {
	return provider.Env{
		GOOS:   "linux",
		Home:   "/home/user",
		Getenv: func(string) string { return "" },
		FS: fstest.MapFS{
			"home/user/.counter/a.jsonl": {Data: []byte("x"), ModTime: time.Now()},
		},
	}
}

func testBudget() func() provider.Budget {
	return func() provider.Budget { return provider.DefaultBudget(time.Now()) }
}

func rescanTestConfig(interval time.Duration) config.Config {
	cfg, err := config.Load("", func(string) string { return "" }, nil)
	if err != nil {
		panic(err)
	}
	cfg.ScanInterval = interval
	return cfg
}

func waitForRescan[T any](t *testing.T, ch <-chan T, description string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
		var zero T
		return zero
	}
}

// TestRunRescanLoopDropsTriggerWhileScanning covers step 13: a trigger that
// arrives while a scan is already in flight is dropped, not queued - it
// never causes a second scan once the first completes.
func TestRunRescanLoopDropsTriggerWhileScanning(t *testing.T) {
	var scans int32
	block := make(chan struct{})
	started := make(chan struct{}, 1)
	completed := make(chan struct{}, 2)
	decisions := make(chan bool, 3)

	reg := provider.NewRegistry()
	if err := reg.Register(countingProvider{count: &scans, block: block, started: started}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	trigger := make(chan struct{}, 2)
	c := collector.New(pricing.Embedded(), collector.Options{})

	go runRescanLoop(ctx, reg, counterEnv(), testBudget(), rescanTestConfig(0), c, trigger, func(dropped bool) {
		decisions <- dropped
	}, func() {
		completed <- struct{}{}
	})

	trigger <- struct{}{} // starts a scan that blocks in Parse until block is closed
	if dropped := waitForRescan(t, decisions, "the first rescan decision"); dropped {
		t.Fatal("first trigger was dropped")
	}
	waitForRescan(t, started, "the first scan to enter Parse")

	trigger <- struct{}{} // must be dropped: the first scan is still in flight
	if dropped := waitForRescan(t, decisions, "the second rescan decision"); !dropped {
		t.Fatal("trigger received while scanning was not dropped")
	}

	close(block) // let the first scan finish
	waitForRescan(t, completed, "the first scan to complete")

	if got := atomic.LoadInt32(&scans); got != 1 {
		t.Fatalf("scans after the dropped trigger = %d, want 1", got)
	}

	// A trigger sent once the loop is idle again must still work: dropping
	// is specific to "a scan is running", not a stuck flag.
	trigger <- struct{}{}
	if dropped := waitForRescan(t, decisions, "the follow-up rescan decision"); dropped {
		t.Fatal("follow-up trigger was dropped after the scan completed")
	}
	waitForRescan(t, completed, "the follow-up scan to complete")
	if got := atomic.LoadInt32(&scans); got != 2 {
		t.Fatalf("scans after the follow-up trigger = %d, want 2", got)
	}
}

// TestRunRescanLoopTickerFiresWithinRange covers step 14: a 50ms
// scan_interval run for 200ms produces between 3 and 5 scans.
func TestRunRescanLoopTickerFiresWithinRange(t *testing.T) {
	var scans int32
	reg := provider.NewRegistry()
	if err := reg.Register(countingProvider{count: &scans}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	trigger := make(chan struct{})
	c := collector.New(pricing.Embedded(), collector.Options{})

	go runRescanLoop(ctx, reg, counterEnv(), testBudget(), rescanTestConfig(50*time.Millisecond), c, trigger, nil, nil)

	time.Sleep(200 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond) // let any in-flight scan settle

	got := atomic.LoadInt32(&scans)
	if got < 3 || got > 5 {
		t.Fatalf("scans in 200ms at a 50ms interval = %d, want between 3 and 5", got)
	}
}

// TestRunRescanLoopPublishesNewResult covers step 13's other half: a
// triggered re-scan actually reaches Collector.Set.
func TestRunRescanLoopPublishesNewResult(t *testing.T) {
	var scans int32
	reg := provider.NewRegistry()
	if err := reg.Register(countingProvider{count: &scans}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	trigger := make(chan struct{}, 1)
	c := collector.New(pricing.Embedded(), collector.Options{})

	go runRescanLoop(ctx, reg, counterEnv(), testBudget(), rescanTestConfig(0), c, trigger, nil, nil)

	if c.Ready() {
		t.Fatal("collector is ready before any scan ran")
	}

	trigger <- struct{}{}
	time.Sleep(50 * time.Millisecond)

	if !c.Ready() {
		t.Fatal("collector is not ready after a triggered scan completed")
	}
}
