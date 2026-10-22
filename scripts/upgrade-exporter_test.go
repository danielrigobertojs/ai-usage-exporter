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
	"strconv"
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
	// On macOS the temporary directory is reached through /tmp, while the
	// kernel reports the resolved /private/tmp path for the processes
	// running from it. Comparing the executable path against the unresolved
	// root would never match, so both sides are resolved here.
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve temporary root: %v", err)
	}
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
	// The upgrade helper replaces the running exporter with a new process it
	// starts detached ("nohup ... &"), so that replacement outlives both the
	// helper and this test. Once the upgrade succeeds the replacement is the
	// process owning the port, therefore stopping only the process started
	// here would leave an orphan holding the port for the rest of the
	// machine's life. The cleanup below stops the replacement too and fails
	// the test if anything is still listening afterwards.
	var replacementPID int
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		stopProcess(replacementPID)
		if pid := waitPortReleased(port, 5*time.Second); pid != 0 {
			// Only signal a listener that is provably one of the fixtures
			// this test built: a process that merely reused the port is
			// reported but never killed.
			if path := processExecutable(pid); strings.HasPrefix(path, resolvedRoot) {
				stopProcess(pid)
			}
			t.Errorf("upgrade left an exporter listening on TCP port %s (pid %d) after the test finished", port, pid)
		}
	})
	waitForBuild(t, port, "old")
	t.Logf("before: curl http://127.0.0.1:%s/metrics | grep build_info => %s", port, currentMetric(t, port))
	t.Logf("operator cwd before: %s", lsLong(t, sentinel))

	upgrade := exec.Command("bash", filepath.Join(scriptDir(t), "upgrade-exporter.sh"), "--port", port, "--new-binary", new)
	upgrade.Dir = operatorDir
	out, err := upgrade.CombinedOutput()
	// The helper reports the PID of the replacement it started. It is read
	// before the failure check on purpose: when the helper fails after the
	// replacement was launched, the replacement is still running and the
	// cleanup has to stop it as well.
	replacementPID = restartedPID(string(out))
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

// restartedPID extracts the PID of the replacement exporter from the output of
// the upgrade helper, which reports it as "Restarted PID=<pid> BINARY_PATH=...".
func restartedPID(output string) int {
	for _, line := range strings.Split(output, "\n") {
		const marker = "Restarted PID="
		index := strings.Index(line, marker)
		if index < 0 {
			continue
		}
		rest := line[index+len(marker):]
		end := strings.IndexAny(rest, " \t")
		if end >= 0 {
			rest = rest[:end]
		}
		pid, err := strconv.Atoi(rest)
		if err != nil {
			continue
		}
		return pid
	}
	return 0
}

// stopProcess kills a process started outside this test's process tree. The
// replacement exporter is detached by the upgrade helper, so it cannot be
// waited for, only signalled.
func stopProcess(pid int) {
	if pid <= 0 {
		return
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = process.Kill()
}

// listenerPID returns the PID of the process listening on the given TCP port,
// or 0 when nothing is listening.
func listenerPID(port string) int {
	out, err := exec.Command("lsof", "-nP", "-iTCP:"+port, "-sTCP:LISTEN", "-t").Output()
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return 0
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0
	}
	return pid
}

// processExecutable returns the path of the executable running in the given
// process, or an empty string when it cannot be determined.
func processExecutable(pid int) string {
	out, err := exec.Command("lsof", "-a", "-p", strconv.Itoa(pid), "-d", "txt", "-Fn").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n") {
			return strings.TrimPrefix(line, "n")
		}
	}
	return ""
}

// waitPortReleased waits until nothing listens on the given TCP port. It
// returns the PID of the remaining listener, or 0 when the port was released.
func waitPortReleased(port string, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	for {
		pid := listenerPID(port)
		if pid == 0 || !time.Now().Before(deadline) {
			return pid
		}
		time.Sleep(100 * time.Millisecond)
	}
}
