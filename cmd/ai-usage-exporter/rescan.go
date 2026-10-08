// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package main

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/collector"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/config"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/scan"
)

// runRescanLoop refreshes the complete snapshot in response to trigger
// (wired to SIGHUP) or, when cfg.ScanInterval > 0, a ticker. The default
// ScanInterval is one minute; setting it to 0 disables the ticker and leaves
// trigger as the only way in.
//
// A trigger or tick that arrives while a scan is already running is
// dropped, never queued: scanning is a single atomic flag, not a buffered
// counter, so a burst of SIGHUPs during a slow scan collapses to at most
// one extra scan, not one per signal.
//
// runRescanLoop blocks until ctx is done.
// onDecision and onComplete are test hooks. Production callers pass nil; tests
// use them to synchronize on the atomic decision and when scanning becomes idle.
func runRescanLoop(ctx context.Context, reg *provider.Registry, env provider.Env, newBudget func() provider.Budget, cfg config.Config, c *collector.Collector, trigger <-chan struct{}, onDecision func(dropped bool), onComplete func()) {
	var tickCh <-chan time.Time
	if cfg.ScanInterval > 0 {
		ticker := time.NewTicker(cfg.ScanInterval)
		defer ticker.Stop()
		tickCh = ticker.C
	}

	var scanning atomic.Bool

	rescan := func() {
		if !scanning.CompareAndSwap(false, true) {
			if onDecision != nil {
				onDecision(true)
			}
			return
		}
		if onDecision != nil {
			onDecision(false)
		}
		go func() {
			defer func() {
				scanning.Store(false)
				if onComplete != nil {
					onComplete()
				}
			}()

			tz, err := cfg.Location()
			if err != nil {
				tz = time.UTC
			}
			result, err := scan.Run(ctx, reg, env, newBudget(), time.Now(), tz)
			if err != nil {
				return
			}
			c.Set(result)
		}()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-trigger:
			rescan()
		case <-tickCh:
			rescan()
		}
	}
}
