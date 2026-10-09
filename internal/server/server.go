// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

// Package server wires a collector.Collector into an *http.Server exposing
// /metrics, /healthz, and /readyz. It owns no scanning logic: the caller is
// responsible for running scan.Run and calling Collector.Set before this
// server's listener ever accepts a connection.
package server

import (
	"log/slog"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/collector"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/config"
)

// New returns an *http.Server with three routes mounted:
//
//   - cfg.MetricsPath (default "/metrics"): c's Prometheus exposition,
//     served from a dedicated Registry holding only c - no Go runtime or
//     process collectors, so the output stays exactly the metrics
//     docs/metrics.md contracts.
//   - "/healthz": always 200 while the process is alive.
//   - "/readyz": 503 until ready() reports true, 200 after. Prometheus must
//     not scrape an empty snapshot and record a false 0 before the first
//     scan has actually completed.
func New(cfg config.Config, c *collector.Collector, ready func() bool) *http.Server {
	reg := prometheus.NewRegistry()
	reg.MustRegister(c)

	mux := http.NewServeMux()
	metrics := promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
	mux.Handle(cfg.MetricsPath, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Debug("metrics scrape served", "method", r.Method)
		metrics.ServeHTTP(w, r)
	}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if !ready() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	return &http.Server{
		Addr:    cfg.Listen,
		Handler: mux,
	}
}
