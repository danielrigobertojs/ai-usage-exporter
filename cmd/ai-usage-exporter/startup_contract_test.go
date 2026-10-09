// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/collector"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/config"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/pricing"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/scan"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/server"
)

// TestDoctorSubcommandLoggingFlagsEndToEnd drives the compiled binary because
// its logger wiring lives in main() and config.Load, which read os.Args and
// the process environment and end in os.Exit. It never touches this machine's
// real provider roots: every test points the tool homes at an empty temporary
// directory. The startup-policy tests below exercise the explicit seam in
// process so their outcome never depends on timer resolution.

// syncBuffer is a bytes.Buffer that os/exec's copier goroutine and the test
// goroutine can both touch. Reading a bare bytes.Buffer while the child is
// still running is a data race, and -race says so.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Bytes()
}

var (
	buildOnce sync.Once
	buildPath string
	buildErr  error
)

// exporterBinary builds cmd/ai-usage-exporter once per package run.
func exporterBinary(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	buildOnce.Do(func() {
		root, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			buildErr = err
			return
		}
		out := filepath.Join(os.TempDir(), "aue-startup-contract-"+fmt.Sprint(os.Getpid()))
		if runtime.GOOS == "windows" {
			out += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", out, "./cmd/ai-usage-exporter")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if combined, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("go build: %v: %s", err, combined)
			return
		}
		buildPath = out
	})
	if buildErr != nil {
		t.Skipf("cannot build the exporter: %v", buildErr)
	}
	return buildPath
}

// isolatedEnv returns an environment whose provider roots are all empty, so
// a scan finds nothing and finishes on its own instead of depending on how
// much history this machine happens to have.
func isolatedEnv(t *testing.T) []string {
	t.Helper()
	base := t.TempDir()
	env := append(os.Environ(),
		"CGO_ENABLED=0",
		"XDG_CONFIG_HOME="+base,
		"XDG_DATA_HOME="+base,
		"CLAUDE_CONFIG_DIR="+base,
		"CODEX_HOME="+base,
		"OPENCODE_DATA_HOME="+base,
	)
	return env
}

// TestDegradedStartupServesMetricsWithoutUsageCounters is the contract
// ADR-007 exists for. A startup scan that cannot finish must leave the
// process serving /metrics with ai_usage_scan_success 0 and no usage
// counters at all: publishing a partial aggregate would make the gauges go
// backwards, which is the false reading the degradation was designed to
// avoid. Before this, the same scan killed the process with exit=1, which
// Prometheus cannot tell apart from the host being down.
func TestDegradedStartupServesMetricsWithoutUsageCounters(t *testing.T) {
	c := collector.New(pricing.Embedded(), collector.Options{})
	cfg := config.Config{MetricsPath: "/metrics"}
	if err := applyStartupScanResult(cfg, c, scan.Result{}, context.DeadlineExceeded, pricing.Embedded(), "scan-under-test"); err != nil {
		t.Fatalf("degraded startup returned error: %v", err)
	}

	req := httptest.NewRequest("GET", cfg.MetricsPath, nil)
	resp := httptest.NewRecorder()
	server.New(cfg, c, c.Ready).Handler.ServeHTTP(resp, req)
	if resp.Code != 200 {
		t.Fatalf("GET %s status = %d, want 200", cfg.MetricsPath, resp.Code)
	}
	body := resp.Body.String()

	if !strings.Contains(body, "\nai_usage_scan_success 0\n") && !strings.HasPrefix(body, "ai_usage_scan_success 0\n") {
		t.Errorf("ai_usage_scan_success is not 0 after a cancelled startup scan:\n%s", body)
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "ai_usage_tokens{") ||
			strings.HasPrefix(line, "ai_usage_cost_usd{") ||
			strings.HasPrefix(line, "ai_usage_sessions{") ||
			strings.HasPrefix(line, "ai_usage_tool_calls{") {
			t.Errorf("usage metric published from a cancelled scan: %s", line)
		}
	}
}

// TestStrictStartupScanErrorExitsNonZero pins the error returned to main by
// the opt-in strict policy; main turns a run error into exit status 1.
func TestStrictStartupScanErrorExitsNonZero(t *testing.T) {
	c := collector.New(pricing.Embedded(), collector.Options{})
	cfg := config.Config{FailOnStartupScanError: true}
	err := applyStartupScanResult(cfg, c, scan.Result{}, context.DeadlineExceeded, pricing.Embedded(), "scan-under-test")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("strict startup error = %v, want context deadline exceeded", err)
	}
}

// TestDoctorSubcommandLoggingFlagsEndToEnd covers the wiring in main() that
// the unit test in internal/cli cannot reach: main has to install the logger
// before Execute does any I/O. Without that call the flags are parsed but
// never applied and doctor logs plain text at INFO whatever was asked for.
func TestDoctorSubcommandLoggingFlagsEndToEnd(t *testing.T) {
	bin := exporterBinary(t)

	cmd := exec.Command(bin, "doctor", "--log-level", "debug", "--log-format", "json", "--output", "json")
	cmd.Env = isolatedEnv(t)
	var stdout, stderr syncBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("exit = %v\nstderr:\n%s", err, stderr.String())
	}
	if !json.Valid(stdout.Bytes()) {
		t.Fatalf("stdout is not valid JSON: %s", stdout.String())
	}
	if !strings.Contains(stderr.String(), `"level":"DEBUG"`) {
		t.Errorf("--log-level debug was not applied to the subcommand; stderr:\n%s", stderr.String())
	}
	var sawScanID bool
	for _, line := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("stderr line is not JSON (%v): %s", err, line)
		}
		id, ok := rec["scan_id"]
		if !ok {
			continue
		}
		sawScanID = true
		if id == "unknown" {
			t.Errorf("scan_id = %q; every CLI invocation must generate one", id)
		}
	}
	if !sawScanID {
		t.Errorf("no line carries a scan_id; stderr:\n%s", stderr.String())
	}
}
