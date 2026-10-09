// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package main

import (
	"context"
	"fmt"
	"log/slog"
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

	rescan := func(reason string) {
		if !scanning.CompareAndSwap(false, true) {
			slog.Debug("rescan dropped; another scan is in progress", "reason", reason)
			if onDecision != nil {
				onDecision(true)
			}
			return
		}
		if onDecision != nil {
			onDecision(false)
		}
		slog.Info("rescan triggered", "reason", reason)
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
			scanCtx, cancel := context.WithTimeout(scan.WithID(ctx, newScanID()), cfg.ScanTimeout)
			defer cancel()
			result, err := scan.Run(scanCtx, reg, env, newBudget(), time.Now(), tz)
			if err != nil {
				slog.Error("rescan failed", "error_type", fmt.Sprintf("%T", err))
				c.MarkScanFailure()
				return
			}
			c.Set(result)
			logSnapshotPublished(scan.ID(scanCtx), result, c.Catalog())
		}()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-trigger:
			rescan("SIGHUP")
		case <-tickCh:
			rescan("ticker")
		}
	}
}
