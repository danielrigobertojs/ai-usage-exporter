// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/cli"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/collector"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/config"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/pricing"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider/all"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/scan"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/server"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] != "serve" {
		os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		os.Args = append(os.Args[:1], os.Args[2:]...)
	}
	if err := run(); err != nil {
		slog.Error("ai-usage-exporter: fatal", "error", err)
		os.Exit(1)
	}
}

// run is the minimal "serve" path: resolve config, scan every provider
// once, publish the result, and start /metrics. Everything beyond that -
// subcommands, provider selection UX, parity with other agent exporters -
// is JCB-315's scope, not this ticket's.
func run() error {
	cfg, err := config.Load("", os.Getenv, os.Args[1:])
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	reg, err := all.Registry()
	if err != nil {
		return fmt.Errorf("provider registry: %w", err)
	}
	reg, err = filterProviders(reg, cfg.Providers)
	if err != nil {
		return fmt.Errorf("provider registry: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	catalog, err := pricing.Load(ctx, cfg.Pricing)
	if err != nil {
		return fmt.Errorf("pricing: %w", err)
	}

	c := collector.New(catalog, collector.Options{ProjectLabel: cfg.Labels.Project})

	env := provider.OSEnv()
	newBudget := func() provider.Budget { return provider.DefaultBudget(time.Now()) }

	tz, err := cfg.Location()
	if err != nil {
		return fmt.Errorf("config: timezone: %w", err)
	}

	scanCtx, scanCancel := context.WithTimeout(ctx, cfg.ScanTimeout)
	result, err := scan.Run(scanCtx, reg, env, newBudget(), time.Now(), tz)
	scanCancel()
	if err != nil {
		return fmt.Errorf("startup scan: %w", err)
	}
	c.Set(result)
	slog.Info("ai-usage-exporter: startup scan complete", "duration", result.Duration, "tools", len(result.PerTool))

	srv := server.New(cfg, c, c.Ready)

	trigger := make(chan struct{}, 1)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-sigCh:
				select {
				case trigger <- struct{}{}:
				default:
				}
			}
		}
	}()

	go runRescanLoop(ctx, reg, env, newBudget, cfg, c, trigger, nil, nil)

	errCh := make(chan error, 1)
	go func() {
		slog.Info("ai-usage-exporter: listening", "addr", cfg.Listen, "path", cfg.MetricsPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case <-stopCh:
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// filterProviders restricts reg to the given IDs, preserving registration
// order. An empty ids means "every registered provider" - the config
// default. An unknown ID is a configuration error: silently ignoring a typo
// here would make a scan look "clean" while quietly skipping a tool the
// operator meant to include.
func filterProviders(reg *provider.Registry, ids []string) (*provider.Registry, error) {
	if len(ids) == 0 {
		return reg, nil
	}

	out := provider.NewRegistry()
	for _, id := range ids {
		p, ok := reg.Get(id)
		if !ok {
			return nil, fmt.Errorf("unknown provider %q", id)
		}
		if err := out.Register(p); err != nil {
			return nil, err
		}
	}
	return out, nil
}
