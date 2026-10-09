// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval
package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider/fake"
)

// hermeticCLI points the CLI at an injected registry and an empty
// filesystem, and hides any user config file. Without this the subcommand
// logging tests below would depend on whatever the developer has in
// ~/.claude and ~/.codex.
func hermeticCLI(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	oldRegistry, oldEnvironment, oldLogger := registry, environment, slog.Default()
	t.Cleanup(func() {
		registry, environment = oldRegistry, oldEnvironment
		slog.SetDefault(oldLogger)
	})
	registry = func() (*provider.Registry, error) { r := provider.NewRegistry(); return r, r.Register(fake.New(1)) }
	environment = func() provider.Env {
		return provider.Env{GOOS: "linux", Home: "/home/test", Getenv: func(string) string { return "" }, FS: fstest.MapFS{}}
	}
}

// decodeLogLines asserts every captured line is a JSON object and returns
// the decoded records.
func decodeLogLines(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(raw, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON (%v): %s", err, line)
		}
		out = append(out, rec)
	}
	return out
}

// TestConfigureLoggerHonoursSubcommandFlags pins the contract that made
// AI_USAGE_LOG_LEVEL inert: ConfigureLogger has to run before Execute does
// any I/O, and it has to accept --log-level / --log-format on every
// subcommand. Before this, doctor emitted plain-text INFO lines no matter
// what the operator asked for.
func TestConfigureLoggerHonoursSubcommandFlags(t *testing.T) {
	hermeticCLI(t)
	logs := &bytes.Buffer{}

	ConfigureLogger([]string{"doctor", "--log-level", "debug", "--log-format", "json"}, logs)

	var out, errOut bytes.Buffer
	if code := Execute([]string{"doctor", "--output", "json"}, &out, &errOut); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if !json.Valid(out.Bytes()) {
		t.Fatalf("stdout is not valid JSON: %s", out.String())
	}

	records := decodeLogLines(t, logs.String())
	if len(records) == 0 {
		t.Fatal("no log lines captured: the subcommand wrote nothing to the logger")
	}
	var withScanID int
	for _, rec := range records {
		id, ok := rec["scan_id"]
		if !ok {
			continue
		}
		withScanID++
		if id == "unknown" {
			t.Errorf("scan_id = %q; every CLI invocation must generate one", id)
		}
	}
	if withScanID == 0 {
		t.Errorf("no line carries a scan_id; logs = %s", logs.String())
	}
	if !hasLevel(records, "DEBUG") {
		t.Errorf("--log-level debug did not take effect; levels seen = %v", levelsSeen(records))
	}
}

// TestConfigureLoggerRespectsErrorLevelOnSubcommands is the scripting
// half: doctor is meant to be run from a shell pipeline, so at error level
// it must say nothing on stderr and leave stdout parseable.
func TestConfigureLoggerRespectsErrorLevelOnSubcommands(t *testing.T) {
	hermeticCLI(t)
	logs := &bytes.Buffer{}

	ConfigureLogger([]string{"doctor", "--log-level", "error", "--log-format", "text"}, logs)

	var out, errOut bytes.Buffer
	if code := Execute([]string{"doctor", "--output", "json"}, &out, &errOut); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	for _, rec := range decodeLogLines(t, logs.String()) {
		if lvl, _ := rec["level"].(string); lvl != "" && lvl != "ERROR" {
			t.Errorf("level = %q, want ERROR or nothing at --log-level error", lvl)
		}
	}
}

// TestExecuteStripsLoggingFlagsBeforeSubcommandParsing guards the other
// half of the arrangement: Execute must still not see --log-level, or
// every subcommand's FlagSet would reject it.
func TestExecuteStripsLoggingFlagsBeforeSubcommandParsing(t *testing.T) {
	hermeticCLI(t)
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))

	var out, errOut bytes.Buffer
	if code := Execute([]string{"doctor", "--log-level", "debug", "--log-format", "json", "--output", "json"}, &out, &errOut); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
}

func hasLevel(records []map[string]any, want string) bool {
	for _, rec := range records {
		if lvl, _ := rec["level"].(string); lvl == want {
			return true
		}
	}
	return false
}

func levelsSeen(records []map[string]any) []string {
	seen := map[string]bool{}
	for _, rec := range records {
		if lvl, _ := rec["level"].(string); lvl != "" {
			seen[lvl] = true
		}
	}
	out := make([]string, 0, len(seen))
	for lvl := range seen {
		out = append(out, lvl)
	}
	return out
}
