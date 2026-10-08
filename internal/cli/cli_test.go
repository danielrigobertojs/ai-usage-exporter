// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/aggregate"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider/fake"
)

func TestExecuteVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if got := Execute([]string{"version"}, &out, &errOut); got != 0 {
		t.Fatalf("exit=%d", got)
	}
	if !strings.Contains(out.String(), "ai-usage-exporter") {
		t.Fatal("version missing")
	}
}
func TestProvidersJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	if got := Execute([]string{"providers", "--output", "json"}, &out, &errOut); got != 0 {
		t.Fatalf("exit=%d: %s", got, errOut.String())
	}
	var rows []providerView
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("providers=%d, want 3", len(rows))
	}
}
func TestReportRejectsWindow(t *testing.T) {
	var out, errOut bytes.Buffer
	if got := Execute([]string{"report", "--window", "bogus"}, &out, &errOut); got != 2 {
		t.Fatalf("exit=%d", got)
	}
	if out.Len() != 0 || !strings.Contains(errOut.String(), "24h") {
		t.Fatalf("out=%q err=%q", out.String(), errOut.String())
	}
}

func TestReportAcceptsOneHourWindow(t *testing.T) {
	if !validWindow(aggregate.Window1h) {
		t.Fatal("1h must be an accepted report window")
	}
}

func TestProvidersRejectsInvalidOutput(t *testing.T) {
	var out, errOut bytes.Buffer
	if got := Execute([]string{"providers", "--output", "bogus"}, &out, &errOut); got != 2 || out.Len() != 0 || !strings.Contains(errOut.String(), "--output") {
		t.Fatalf("exit=%d out=%q err=%q", got, out.String(), errOut.String())
	}
}

func TestDoctorRejectsUnknownTool(t *testing.T) {
	var out, errOut bytes.Buffer
	if got := Execute([]string{"doctor", "--tool", "bogus"}, &out, &errOut); got != 2 || out.Len() != 0 || !strings.Contains(errOut.String(), `unknown provider "bogus"`) {
		t.Fatalf("exit=%d out=%q err=%q", got, out.String(), errOut.String())
	}
}
func TestExecuteRequiresSubcommand(t *testing.T) {
	for _, args := range [][]string{nil, {"serve"}} {
		var out, errOut bytes.Buffer
		if got := Execute(args, &out, &errOut); got != 2 || errOut.Len() == 0 {
			t.Fatalf("args=%v exit=%d stderr=%q", args, got, errOut.String())
		}
	}
}

func TestDoctorReportsUnavailableInjectedProvider(t *testing.T) {
	oldRegistry, oldEnvironment := registry, environment
	t.Cleanup(func() { registry, environment = oldRegistry, oldEnvironment })
	registry = func() (*provider.Registry, error) { r := provider.NewRegistry(); return r, r.Register(fake.New(1)) }
	environment = func() provider.Env {
		return provider.Env{GOOS: "linux", Home: "/home/test", Getenv: func(string) string { return "" }, FS: fstest.MapFS{}}
	}
	var out, errOut bytes.Buffer
	if got := Execute([]string{"doctor", "--output", "json"}, &out, &errOut); got != 0 {
		t.Fatalf("exit=%d stderr=%s", got, errOut.String())
	}
	if !strings.Contains(out.String(), `"available":false`) || !strings.Contains(out.String(), `"hint"`) {
		t.Fatalf("doctor output=%s", out.String())
	}
}

func TestReportJSONUnknownPriceWithInjectedProvider(t *testing.T) {
	oldRegistry, oldEnvironment := registry, environment
	t.Cleanup(func() { registry, environment = oldRegistry, oldEnvironment })
	registry = func() (*provider.Registry, error) { r := provider.NewRegistry(); return r, r.Register(fake.New(1)) }
	environment = func() provider.Env {
		return provider.Env{GOOS: "linux", Home: "/home/test", Getenv: func(string) string { return "" }, FS: fstest.MapFS{"home/test/.fake/a.jsonl": {Data: []byte("x"), ModTime: time.Now()}}}
	}
	var out, errOut bytes.Buffer
	if got := Execute([]string{"report", "--output", "json"}, &out, &errOut); got != 0 {
		t.Fatalf("exit=%d stderr=%s", got, errOut.String())
	}
	var result reportView
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) == 0 || result.Rows[0].CostUSD != nil {
		t.Fatalf("rows=%+v, want unknown cost", result.Rows)
	}
}

func TestDoctorJSONReportsBudgetHit(t *testing.T) {
	oldRegistry, oldEnvironment := registry, environment
	t.Cleanup(func() { registry, environment = oldRegistry, oldEnvironment })
	registry = func() (*provider.Registry, error) { r := provider.NewRegistry(); return r, r.Register(fake.New(0)) }
	files := make(fstest.MapFS, 20_001)
	for i := 0; i < 20_001; i++ {
		files[fmt.Sprintf("home/test/.fake/%05d.jsonl", i)] = &fstest.MapFile{Data: []byte("x"), ModTime: time.Unix(int64(i), 0)}
	}
	environment = func() provider.Env {
		return provider.Env{GOOS: "linux", Home: "/home/test", Getenv: func(string) string { return "" }, FS: files}
	}
	var out, errOut bytes.Buffer
	if got := Execute([]string{"doctor", "--output", "json"}, &out, &errOut); got != 0 {
		t.Fatalf("exit=%d stderr=%s", got, errOut.String())
	}
	var rows []doctorRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !rows[0].BudgetHit || rows[0].SkippedByBudget != 1 {
		t.Fatalf("rows=%+v, want budget_hit with one budget skip", rows)
	}
}

func TestCLIDoesNotUseFmtPrintWithoutWriter(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), entry.Name(), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", entry.Name(), err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if ok && pkg.Name == "fmt" && (sel.Sel.Name == "Print" || sel.Sel.Name == "Printf" || sel.Sel.Name == "Println") {
				t.Errorf("%s uses fmt.%s without an injected writer", entry.Name(), sel.Sel.Name)
			}
			return true
		})
	}
}

func TestReportJSONStaysParseableWhenWarningsUseStderr(t *testing.T) {
	oldRegistry, oldEnvironment := registry, environment
	t.Cleanup(func() { registry, environment = oldRegistry, oldEnvironment })
	registry = func() (*provider.Registry, error) {
		r := provider.NewRegistry()
		return r, r.Register(failingProvider{})
	}
	environment = func() provider.Env {
		return provider.Env{GOOS: "linux", Home: "/home/test", Getenv: func(string) string { return "" }, FS: fstest.MapFS{"home/test/.broken/a.jsonl": {Data: []byte("x"), ModTime: time.Now()}}}
	}
	var out, errOut bytes.Buffer
	if got := Execute([]string{"report", "--output", "json"}, &out, &errOut); got != 0 {
		t.Fatalf("exit=%d stderr=%s", got, errOut.String())
	}
	var result reportView
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not JSON: %v; stdout=%q", err, out.String())
	}
	if !strings.Contains(errOut.String(), "warning: broken: 1 parse errors") {
		t.Fatalf("stderr=%q, want parse warning", errOut.String())
	}
}

func TestDoctorReportsFirstParseErrorAndResolvedRoots(t *testing.T) {
	oldRegistry, oldEnvironment := registry, environment
	t.Cleanup(func() { registry, environment = oldRegistry, oldEnvironment })
	registry = func() (*provider.Registry, error) {
		r := provider.NewRegistry()
		return r, r.Register(failingProvider{})
	}
	environment = func() provider.Env {
		return provider.Env{GOOS: "linux", Home: "/home/test", Getenv: func(string) string { return "" }, FS: fstest.MapFS{"home/test/.broken/a.jsonl": {Data: []byte("x"), ModTime: time.Now()}}}
	}
	var out, errOut bytes.Buffer
	if got := Execute([]string{"doctor", "--output", "json"}, &out, &errOut); got != 0 {
		t.Fatalf("exit=%d stderr=%s", got, errOut.String())
	}
	var rows []doctorRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].FirstParseError != "synthetic parse error" || len(rows[0].Roots) != 1 || rows[0].Roots[0] != "/home/test/.broken" {
		t.Fatalf("rows=%+v, want resolved root and first parse error", rows)
	}
}

type failingProvider struct{}

func (failingProvider) Descriptor() provider.Descriptor {
	return provider.Descriptor{ID: "broken", DisplayName: "Broken Provider", Kind: provider.SourceJSONL, Roots: []provider.RootSpec{{Base: provider.BaseHome, Rel: ".broken", Glob: "*.jsonl"}}}
}

func (failingProvider) Parse(context.Context, provider.Source, func(model.UsageEvent) error) error {
	return errors.New("synthetic parse error")
}
