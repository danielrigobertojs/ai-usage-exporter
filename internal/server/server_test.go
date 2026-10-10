// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/collector"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/config"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/pricing"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/scan"
)

func testConfig() config.Config {
	cfg, err := config.Load("", func(string) string { return "" }, nil)
	if err != nil {
		panic(err)
	}
	return cfg
}

// TestReadyzReflectsCollectorState covers step 11: /readyz is 503 before
// the collector's first Set and 200 after; /healthz is always 200.
func TestReadyzReflectsCollectorState(t *testing.T) {
	c := collector.New(pricing.Embedded(), collector.Options{})
	srv := New(testConfig(), c, c.Ready)
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	assertStatus(t, ts.URL+"/healthz", http.StatusOK)
	assertStatus(t, ts.URL+"/readyz", http.StatusServiceUnavailable)

	c.Set(scan.Result{})

	assertStatus(t, ts.URL+"/readyz", http.StatusOK)
	assertStatus(t, ts.URL+"/healthz", http.StatusOK)
}

func TestMetricsPathIsConfigurable(t *testing.T) {
	cfg := testConfig()
	cfg.MetricsPath = "/custom-metrics"
	c := collector.New(pricing.Embedded(), collector.Options{})
	c.Set(scan.Result{})

	srv := New(cfg, c, c.Ready)
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	assertStatus(t, ts.URL+"/custom-metrics", http.StatusOK)

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /metrics status = %d, want %d (only the configured path is mounted)", resp.StatusCode, http.StatusNotFound)
	}
}

func TestServerPrivacyAndResourceLimits(t *testing.T) {
	c := collector.New(pricing.Embedded(), collector.Options{})
	c.Set(scan.Result{})
	srv := New(testConfig(), c, c.Ready)
	if srv.ReadTimeout == 0 || srv.WriteTimeout == 0 || srv.ReadHeaderTimeout == 0 || srv.MaxHeaderBytes == 0 {
		t.Fatalf("server safety limits are incomplete: %+v", srv)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	assertStatus(t, ts.URL+"/debug/pprof/", http.StatusNotFound)
}

// TestMetricsOnlyExposesTheContractNamespace covers the acceptance
// criterion that /metrics returns the 11 ai_usage_* metrics and nothing
// else - no Go runtime or process collector output, since New registers c
// on a dedicated Registry.
func TestMetricsOnlyExposesTheContractNamespace(t *testing.T) {
	c := collector.New(pricing.Embedded(), collector.Options{})
	c.Set(scan.Result{})

	srv := New(testConfig(), c, c.Ready)
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name := strings.SplitN(line, "{", 2)[0]
		name = strings.SplitN(name, " ", 2)[0]
		if !strings.HasPrefix(name, "ai_usage_") {
			t.Errorf("/metrics exposes %q, which is outside the ai_usage_ namespace", name)
		}
	}
}

// slowProvider blocks in Parse until unblock is closed, simulating a
// provider that takes a while to read its logs.
type slowProvider struct {
	unblock chan struct{}
}

func (s slowProvider) Descriptor() provider.Descriptor {
	return provider.Descriptor{
		ID:   "slow",
		Kind: provider.SourceJSONL,
		Roots: []provider.RootSpec{
			{Base: provider.BaseHome, Rel: ".slow", Glob: "*.jsonl"},
		},
	}
}

func (s slowProvider) Parse(ctx context.Context, src provider.Source, emit func(model.UsageEvent) error) error {
	<-s.unblock
	return emit(model.UsageEvent{
		Key:       model.EventKey{Tool: "slow", SessionID: src.Path, MessageID: src.Path + "#0"},
		Tool:      "slow",
		Model:     "slow-model",
		Timestamp: src.ModTime,
		Tokens:    map[model.TokenClass]int64{model.TokenInput: 42},
	})
}

// TestFirstRequestAfterStartupScanAlreadySeesData covers step 12: Run a
// slow provider's scan to completion and Set the Collector before the
// listener ever accepts a connection; the very first /metrics request must
// already see that data, never an empty snapshot.
func TestFirstRequestAfterStartupScanAlreadySeesData(t *testing.T) {
	unblock := make(chan struct{})
	reg := provider.NewRegistry()
	if err := reg.Register(slowProvider{unblock: unblock}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	env := fakeEnv(t)
	budget := provider.DefaultBudget(time.Now().Add(time.Minute))

	resultCh := make(chan scan.Result, 1)
	go func() {
		close(unblock) // let Parse proceed; the point under test is ordering, not duration
		result, err := scan.Run(context.Background(), reg, env, budget, time.Now(), time.UTC)
		if err != nil {
			t.Errorf("Run: unexpected error: %v", err)
		}
		resultCh <- result
	}()
	result := <-resultCh

	c := collector.New(pricing.Embedded(), collector.Options{})
	c.Set(result) // Set completes before the listener below ever accepts.

	cfg := testConfig()
	cfg.Listen = "127.0.0.1:0"
	srv := New(cfg, c, c.Ready)

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	go srv.Serve(ln)

	resp, err := http.Get("http://" + ln.Addr().String() + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), `tool="slow"`) {
		t.Errorf("first /metrics response does not contain the slow provider's data:\n%s", body)
	}
}

func fakeEnv(t *testing.T) provider.Env {
	t.Helper()
	modTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return provider.Env{
		GOOS: "linux",
		Home: "/home/user",
		Getenv: func(string) string {
			return ""
		},
		FS: fstest.MapFS{
			"home/user/.slow/a.jsonl": {Data: []byte("x"), ModTime: modTime},
		},
	}
}

func assertStatus(t *testing.T, url string, want int) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		t.Errorf("GET %s: status = %d, want %d", url, resp.StatusCode, want)
	}
}
