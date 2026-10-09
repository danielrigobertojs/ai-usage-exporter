// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests drive the compiled binary rather than run() directly: the
// startup contract they pin - degrade instead of dying, serve /metrics
// without publishing a partial aggregate, exit non-zero only when asked -
// lives in main() and config.Load, which read os.Args and the process
// environment and end in os.Exit. Nothing below can be observed by calling
// the function, and nothing below touches this machine's real provider
// roots: every test points the tool homes at an empty temporary directory.

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

// freeAddr returns a loopback address that was bindable a moment ago. The
// exporter logs cfg.Listen rather than the address net.Listen resolved, so a
// :0 port would be unreachable from the test.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("release reserved port: %v", err)
	}
	return addr
}

// waitForMetrics polls addr until /metrics answers or the deadline passes.
func waitForMetrics(t *testing.T, addr string) string {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://" + addr + "/metrics")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return string(body)
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the exporter never served /metrics; it should degrade, not die")
	return ""
}

// TestDegradedStartupServesMetricsWithoutUsageCounters is the contract
// ADR-007 exists for. A startup scan that cannot finish must leave the
// process serving /metrics with ai_usage_scan_success 0 and no usage
// counters at all: publishing a partial aggregate would make the gauges go
// backwards, which is the false reading the degradation was designed to
// avoid. Before this, the same scan killed the process with exit=1, which
// Prometheus cannot tell apart from the host being down.
func TestDegradedStartupServesMetricsWithoutUsageCounters(t *testing.T) {
	bin := exporterBinary(t)
	addr := freeAddr(t)

	// 1ns: the scan context is already expired when scan.Run checks it, so
	// the degradation is deterministic instead of racing this machine's disk.
	cmd := exec.Command(bin, "--scan-timeout", "1ns", "--listen", addr)
	cmd.Env = isolatedEnv(t)
	var logs syncBuffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	body := waitForMetrics(t, addr)

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
	if !strings.Contains(logs.String(), "startup scan failed") {
		t.Errorf("the degradation was not logged:\n%s", logs.String())
	}
}

// TestStrictStartupScanErrorExitsNonZero pins the opt-in escape hatch.
// Default behaviour changed from fail-fast to degrade; an operator who
// depended on the old semantics must be able to get them back with a flag
// instead of a patch.
func TestStrictStartupScanErrorExitsNonZero(t *testing.T) {
	bin := exporterBinary(t)

	cmd := exec.Command(bin, "--scan-timeout", "1ns", "--fail-on-startup-scan-error", "--listen", freeAddr(t))
	cmd.Env = isolatedEnv(t)
	var logs syncBuffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("expected a non-zero exit, got %v; logs:\n%s", err, logs.String())
		}
		if code := exitErr.ExitCode(); code != 1 {
			t.Errorf("exit code = %d, want 1; logs:\n%s", code, logs.String())
		}
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("--fail-on-startup-scan-error did not stop the process; logs:\n%s", logs.String())
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
