// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
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
	if len(os.Args) > 1 && isCLICommand(os.Args[1]) {
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

// isCLICommand keeps flags on the historical default-serve path.
func isCLICommand(first string) bool {
	return first != "serve" && !strings.HasPrefix(first, "-")
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
	configureLogger(cfg)
	slog.Info("configuration loaded", "log_level", cfg.LogLevel, "log_format", cfg.LogFormat)

	reg, err := all.Registry()
	if err != nil {
		return fmt.Errorf("provider registry: %w", err)
	}
	ids := make([]string, 0, len(reg.All()))
	for _, p := range reg.All() {
		ids = append(ids, p.Descriptor().ID)
	}
	slog.Info("providers registered", "providers", strings.Join(ids, ","))
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
	slog.Info("pricing catalog loaded", "source", catalog.Source())

	c := collector.New(catalog, collector.Options{ProjectLabel: cfg.Labels.Project})

	env := provider.OSEnv()
	newBudget := func() provider.Budget { return provider.DefaultBudget(time.Now()) }

	tz, err := cfg.Location()
	if err != nil {
		return fmt.Errorf("config: timezone: %w", err)
	}

	scanCtx, scanCancel := context.WithTimeout(scan.WithID(ctx, newScanID()), cfg.ScanTimeout)
	result, err := scan.Run(scanCtx, reg, env, newBudget(), time.Now(), tz)
	scanCancel()
	if err != nil {
		return fmt.Errorf("startup scan: %w", err)
	}
	c.Set(result)
	slog.Info("snapshot published", "events", eventCount(result), "tokens", tokenCount(result), "series", len(result.Snapshot.Tokens))

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
				slog.Info("rescan signal received", "signal", "SIGHUP")
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
		slog.Info("graceful shutdown requested")
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func configureLogger(cfg config.Config) {
	level := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	opts := &slog.HandlerOptions{Level: level}
	if cfg.LogFormat == "json" {
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, opts)))
		return
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, opts)))
}

func newScanID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err == nil {
		return hex.EncodeToString(b)
	}
	return fmt.Sprintf("%x", time.Now().UnixNano())
}

func eventCount(r scan.Result) int { return len(r.Snapshot.LastEventAt) }
func tokenCount(r scan.Result) int64 {
	var total int64
	for _, n := range r.Snapshot.Tokens {
		total += n
	}
	return total
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
