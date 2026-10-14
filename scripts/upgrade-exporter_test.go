// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package scripts_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestUpgradeExporterResolvesRelativeExecutableOutsideOperatorCWD(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the upgrade helper targets POSIX hosts")
	}
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skipf("lsof is required by the production upgrade helper: %v", err)
	}

	root := t.TempDir()
	serverDir := filepath.Join(root, "server")
	operatorDir := filepath.Join(root, "operator")
	mustMkdir(t, serverDir)
	mustMkdir(t, operatorDir)
	old := filepath.Join(serverDir, "ai-usage-exporter")
	new := filepath.Join(root, "replacement")
	buildFixtureExporter(t, old, "old")
	buildFixtureExporter(t, new, "new")
	sentinel := filepath.Join(operatorDir, "ai-usage-exporter")
	mustWrite(t, sentinel, "do not replace me\n")

	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "./ai-usage-exporter", "--port", port)
	cmd.Dir = serverDir
	if err := cmd.Start(); err != nil {
		t.Fatalf("start relative exporter: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	waitForBuild(t, port, "old")
	t.Logf("before: curl http://127.0.0.1:%s/metrics | grep build_info => %s", port, currentMetric(t, port))
	t.Logf("operator cwd before: %s", lsLong(t, sentinel))

	upgrade := exec.Command("bash", filepath.Join(scriptDir(t), "upgrade-exporter.sh"), "--port", port, "--new-binary", new)
	upgrade.Dir = operatorDir
	out, err := upgrade.CombinedOutput()
	if err != nil {
		t.Fatalf("upgrade failed: %v\n%s", err, out)
	}
	resolvedOld, err := filepath.EvalSymlinks(old)
	if err != nil {
		t.Fatalf("resolve fixture path: %v", err)
	}
	if !strings.Contains(string(out), "BINARY_PATH="+resolvedOld) {
		t.Fatalf("upgrade did not report the original absolute binary path:\n%s", out)
	}
	waitForBuild(t, port, "new")
	t.Logf("after: curl http://127.0.0.1:%s/metrics | grep build_info => %s", port, currentMetric(t, port))
	got, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("read operator-cwd sentinel: %v", err)
	}
	if string(got) != "do not replace me\n" {
		t.Fatalf("operator-cwd binary was overwritten: %q", got)
	}
	t.Logf("operator cwd after: %s", lsLong(t, sentinel))
}

func scriptDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return wd
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func buildFixtureExporter(t *testing.T, output, build string) {
	t.Helper()
	source := filepath.Join(t.TempDir(), "main.go")
	mustWrite(t, source, fmt.Sprintf(`package main
import ("fmt"; "net/http"; "os")
func main() { p := os.Args[2]; http.ListenAndServe(":"+p, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, %q) })) }
`, `ai_usage_build_info{commit="`+build+`"} 1\n`))
	cmd := exec.Command("go", "build", "-o", output, source)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, out)
	}
}

func currentMetric(t *testing.T, port string) string {
	t.Helper()
	response, err := http.Get("http://127.0.0.1:" + port + "/metrics") // #nosec G107 -- localhost test fixture.
	if err != nil {
		t.Fatalf("fetch fixture metrics: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read fixture metrics: %v", err)
	}
	return strings.TrimSpace(string(body))
}

func lsLong(t *testing.T, path string) string {
	t.Helper()
	out, err := exec.Command("ls", "-l", path).Output()
	if err != nil {
		t.Fatalf("ls -l %s: %v", path, err)
	}
	return strings.TrimSpace(string(out))
}

func freePort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer listener.Close()
	return fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)
}

func waitForBuild(t *testing.T, port, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get("http://127.0.0.1:" + port + "/metrics") // #nosec G107 -- localhost test fixture.
		if err == nil {
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if readErr == nil && response.StatusCode == http.StatusOK && strings.Contains(string(body), `commit="`+want+`"`) {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("exporter on port %s never returned %q", port, want)
}
