// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

// Package scripts_test pins the behaviour of gen-third-party.sh.
//
// The generator is the only thing standing between a copyleft or unlicensed
// dependency and the release image, and CI only ever observes it through
// `bash scripts/gen-third-party.sh && git diff --exit-code`. That makes it
// easy to write a change that is deterministic (so the job goes green) while
// quietly dropping the check that was the point of the job. JCB-321, JCB-322
// and JCB-324 are all that same mistake made three times.
//
// These tests therefore drive the real script rather than a reimplementation of
// its rules: a divergence between this file and the script shows up as a test
// failure, not as a green suite describing behaviour the script does not have.
package scripts_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scriptPath locates the generator relative to this test file so the suite
// works regardless of the working directory `go test` picks.
func scriptPath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	p := filepath.Join(wd, "gen-third-party.sh")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("cannot find gen-third-party.sh next to the test: %v", err)
	}
	return p
}

// runScript executes the generator inside dir and returns its combined output
// and exit code. The generator refuses to run outside a git work tree (it calls
// `git rev-parse --show-toplevel` and reads scripts/ via that path), so the
// caller supplies a scratch repository rather than a bare temp dir.
func runScript(t *testing.T, dir string, env ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("bash", scriptPath(t))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errorAs(err, &ee) {
			t.Fatalf("running generator: %v", err)
		}
		code = ee.ExitCode()
	}
	return out.String(), code
}

func errorAs(err error, target **exec.ExitError) bool {
	ee, ok := err.(*exec.ExitError)
	if ok {
		*target = ee
	}
	return ok
}

// TestGeneratorFailsOnUnlicensedDependency is the invariant CI depends on and
// the one a determinism fix is most likely to erode: a dependency the analyzer
// cannot attribute must break the build. The stub reports it as Unknown in both
// the URL and the license columns, which is how go-licenses represents "no
// license found", and the generator must reject it.
func TestGeneratorFailsOnUnlicensedDependency(t *testing.T) {
	for _, tc := range []struct {
		name     string
		license  string
		wantWord string
	}{
		{"unknown license", "Unknown", "has no detectable license"},
		{"empty license", "", "has no detectable license"},
		{"MPL-2.0", "MPL-2.0", "under a copyleft license"},
		{"GPL-3.0", "GPL-3.0", "under a copyleft license"},
		{"LGPL-2.1", "LGPL-2.1", "under a copyleft license"},
		{"AGPL-3.0-only", "AGPL-3.0-only", "under a copyleft license"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := newScratchRepo(t)
			module := "gopkg.in/qa.underlicensed/" + strings.ReplaceAll(tc.name, " ", "-")
			stub := stubToolchain(t, module+",https://example.invalid/LICENSE,"+tc.license)

			out, code := runScript(t, dir, "PATH="+stub+":"+os.Getenv("PATH"))

			if code == 0 {
				t.Fatalf("generator accepted an unlicensed/copyleft dependency (exit 0).\noutput:\n%s", out)
			}
			if !strings.Contains(out, tc.wantWord) {
				t.Errorf("exit code %d but output does not mention %q.\noutput:\n%s", code, tc.wantWord, out)
			}
		})
	}
}

// TestGeneratorKeepsURLBackedRecord covers the JCB-342 defect itself:
// go-licenses intermittently returns the same module twice, once with a license
// URL and once with Unknown in that column. Before the fix both rows reached the
// table and the file grew a line, which CI saw as a diff.
func TestGeneratorKeepsURLBackedRecord(t *testing.T) {
	dir := newScratchRepo(t)
	stub := stubToolchain(t, strings.Join([]string{
		"gopkg.in/yaml.v3,https://github.com/go-yaml/yaml/blob/v3.0.1/LICENSE,MIT",
		"gopkg.in/yaml.v3,Unknown,MIT",
	}, "\n"))

	out, code := runScript(t, dir, "PATH="+stub+":"+os.Getenv("PATH"))
	if code != 0 {
		t.Fatalf("generator rejected a module that has license evidence (exit %d).\noutput:\n%s", code, out)
	}

	got := countRows(t, dir, "gopkg.in/yaml.v3")
	if got != 1 {
		t.Errorf("module emitted %d times, want exactly 1: the Unknown-URL duplicate must not become a second row", got)
	}
}

// TestGeneratorDeterministicAcrossRuns feeds the generator the same csv twice and
// requires byte-identical output. The stub is what makes this deterministic and
// reproducible, unlike the real analyzer whose Unknown verdict is intermittent.
func TestGeneratorDeterministicAcrossRuns(t *testing.T) {
	dir := newScratchRepo(t)
	stub := stubToolchain(t, strings.Join([]string{
		"gopkg.in/yaml.v3,https://github.com/go-yaml/yaml/blob/v3.0.1/LICENSE,MIT",
		"gopkg.in/yaml.v3,Unknown,MIT",
		"github.com/qa/clean,https://example.invalid/LICENSE,MIT",
	}, "\n"))

	env := []string{"PATH=" + stub + ":" + os.Getenv("PATH")}
	if _, code := runScript(t, dir, env...); code != 0 {
		t.Fatalf("first run failed (exit %d)", code)
	}
	first := readOutput(t, dir)

	if _, code := runScript(t, dir, env...); code != 0 {
		t.Fatalf("second run failed (exit %d)", code)
	}
	second := readOutput(t, dir)

	if first != second {
		t.Errorf("same input produced two different files.\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

// TestGeneratorRejectsAmbiguousRecords covers the hole the current generator has
// and the fix in 11c05d3 did not close: a csv row with fewer than three columns.
//
// Before the fix such a row survived into the license check, where the empty
// license column failed the build. The current generator drops every NF<3 row in
// its awk END block without looking at it, so an unlicensed dependency
// represented by a short row now exits 0 and leaves no trace. go-licenses v1.6.0
// emits exactly three columns (report.go writes {Name, LicenseURL, LicenseName}
// through encoding/csv), so this is unreachable today and is a guard against a
// future change in the tool's format rather than a live bug.
func TestGeneratorRejectsAmbiguousRecords(t *testing.T) {
	for _, tc := range []struct {
		name string
		row  string
	}{
		{"two columns, no license column", "gopkg.in/qa.short.v1,Unknown"},
		{"module name only", "gopkg.in/qa.bare.v2"},
		{"healthy module plus a short unlicensed row", "github.com/qa/clean,https://example.invalid/LICENSE,MIT\ngopkg.in/qa.short.v1,Unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := newScratchRepo(t)
			stub := stubToolchain(t, tc.row)

			out, code := runScript(t, dir, "PATH="+stub+":"+os.Getenv("PATH"))

			if code == 0 {
				t.Fatalf("generator exited 0 on a malformed license record %q; a row that cannot be "+
					"read as module,url,license must fail loudly instead of being dropped.\noutput:\n%s", tc.row, out)
			}
		})
	}
}

// TestGeneratorRejectsConflictingLicenses makes sure that ambiguity between two
// records for one module is reported rather than resolved by picking a winner.
func TestGeneratorRejectsConflictingLicenses(t *testing.T) {
	for _, tc := range []struct {
		name string
		csv  string
	}{
		{
			"differing licenses",
			"github.com/qa/conflict,https://example.invalid/LICENSE,MIT\ngithub.com/qa/conflict,https://example.invalid/LICENSE,Apache-2.0",
		},
		{
			"two concrete license urls",
			"github.com/qa/twourl,https://example.invalid/a/LICENSE,MIT\ngithub.com/qa/twourl,https://example.invalid/b/LICENSE,MIT",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := newScratchRepo(t)
			stub := stubToolchain(t, tc.csv)

			out, code := runScript(t, dir, "PATH="+stub+":"+os.Getenv("PATH"))

			if code == 0 {
				t.Fatalf("generator exited 0 on ambiguous licensing evidence; it must fail instead of "+
					"emitting one of the two rows.\noutput:\n%s", out)
			}
		})
	}
}

// --- scratch repository plumbing -------------------------------------------

// newScratchRepo builds a minimal git work tree the generator will accept: it
// cds to `git rev-parse --show-toplevel` and expects scripts/gen-third-party.sh
// and a Makefile exposing `release-platforms`.
func newScratchRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	mustWrite(t, filepath.Join(dir, "go.mod"), "module github.com/qa/scratch\n\ngo 1.24\n")
	mustWrite(t, filepath.Join(dir, "Makefile"), ".PHONY: release-platforms\nrelease-platforms:\n\t@printf 'linux/amd64\\n'\n")
	mustWrite(t, filepath.Join(dir, "scripts", "gen-third-party.sh"), readScript(t))

	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "qa@example.invalid"},
		{"config", "user.name", "qa"},
		{"add", "-A"},
		{"commit", "-q", "-m", "scratch"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func readScript(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(scriptPath(t))
	if err != nil {
		t.Fatalf("reading generator: %v", err)
	}
	return string(b)
}

// stubToolchain puts a fake `go` first on PATH that satisfies the generator's
// `go install github.com/google/go-licenses@v1.6.0` by dropping a stub binary
// into $GOBIN, and forwards every other invocation to the real toolchain so the
// script's own `go list -m` keeps working.
//
// Shadowing `go install` rather than `go-licenses` on PATH is deliberate: the
// generator runs the binary it just built ("$tool_dir/go-licenses"), so a stub
// placed on PATH would simply be ignored.
func stubToolchain(t *testing.T, csv string) string {
	t.Helper()

	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("go toolchain not available: %v", err)
	}

	bin := t.TempDir()
	// The stub that lands in $GOBIN must be a shell script that *prints* the
	// csv; writing the csv itself would make the file non-executable text and
	// bash would try to run the first field as a command. The stub body is
	// assembled with printf rather than a nested heredoc, which would terminate
	// the outer heredoc early on the shared delimiter.
	goLicensesStub := "#!/usr/bin/env bash\nprintf '%s\\n' " + shellQuote(csv) + "\n"
	stubBody := "#!/usr/bin/env bash\n" +
		"# Stub installed by scripts/gen-third-party_test.go.\n" +
		"if [[ \"${1:-}\" == \"install\" ]]; then\n" +
		"  mkdir -p \"$GOBIN\"\n" +
		"  cat > \"$GOBIN/go-licenses\" <<'STUB_EOF'\n" + goLicensesStub + "STUB_EOF\n" +
		"  chmod +x \"$GOBIN/go-licenses\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exec " + shellQuote(realGo) + " \"$@\"\n"

	mustWrite(t, filepath.Join(bin, "go"), stubBody)
	if err := os.Chmod(filepath.Join(bin, "go"), 0o755); err != nil {
		t.Fatalf("chmod go stub: %v", err)
	}
	return bin
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func readOutput(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "THIRD_PARTY_LICENSES.md"))
	if err != nil {
		t.Fatalf("reading generated file: %v", err)
	}
	return string(b)
}

func countRows(t *testing.T, dir, module string) int {
	t.Helper()
	n := 0
	for _, line := range strings.Split(readOutput(t, dir), "\n") {
		if strings.Contains(line, "| "+module+" |") {
			n++
		}
	}
	return n
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
