// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package scan

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/aggregate"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider/opencode"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider/opencode/testdata"
)

// namedFake behaves like internal/provider/fake, but with an ID this test
// package controls, so two (or fifty) instances can be registered side by
// side - the real fake.Provider hardcodes "fake" and would collide.
type namedFake struct {
	id              string
	eventsPerSource int
	failParse       bool
}

func (f namedFake) Descriptor() provider.Descriptor {
	return provider.Descriptor{
		ID:   f.id,
		Kind: provider.SourceJSONL,
		Roots: []provider.RootSpec{
			{Base: provider.BaseHome, Rel: "." + f.id, Glob: "*.jsonl"},
		},
		Capabilities: provider.Capabilities{HasTokens: true, HasToolCalls: true},
	}
}

func (f namedFake) Parse(ctx context.Context, src provider.Source, emit func(model.UsageEvent) error) error {
	if f.failParse {
		return fmt.Errorf("namedFake %s: simulated parse failure", f.id)
	}
	for i := 0; i < f.eventsPerSource; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		evt := model.UsageEvent{
			Key: model.EventKey{
				Tool:      f.id,
				SessionID: src.Path,
				MessageID: fmt.Sprintf("%s#%d", src.Path, i),
			},
			Tool:      f.id,
			Model:     "fake-model",
			Timestamp: src.ModTime.Add(time.Duration(i) * time.Second).UTC(),
			Tokens: map[model.TokenClass]int64{
				model.TokenInput:  int64(10 + i),
				model.TokenOutput: int64(5 + i),
			},
			ToolCalls: 1,
		}
		if err := emit(evt); err != nil {
			return err
		}
	}
	return nil
}

func fakeEnv(id string) provider.Env {
	modTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fs := fstest.MapFS{
		"home/user/." + id + "/a.jsonl": {Data: []byte("x"), ModTime: modTime},
	}
	return provider.Env{
		GOOS:   "linux",
		Home:   "/home/user",
		Getenv: func(string) string { return "" },
		FS:     fs,
	}
}

func multiEnv(ids ...string) provider.Env {
	modTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fs := fstest.MapFS{}
	for _, id := range ids {
		fs["home/user/."+id+"/a.jsonl"] = &fstest.MapFile{Data: []byte("x"), ModTime: modTime}
	}
	return provider.Env{
		GOOS:   "linux",
		Home:   "/home/user",
		Getenv: func(string) string { return "" },
		FS:     fs,
	}
}

func testBudget() provider.Budget {
	return provider.DefaultBudget(time.Now())
}

// osRootFS mirrors provider.realFS for a test that needs a real SQLite file.
// Env.FS receives virtual slash-separated paths without a leading slash; on
// Windows those paths retain the drive letter and cannot be opened through
// os.DirFS("/").
type osRootFS struct {
	goos string
}

func (r osRootFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if r.goos == "windows" {
		return os.Open(strings.ReplaceAll(name, "/", `\`))
	}
	return os.Open("/" + name)
}

// TestRunSumsAcrossProvidersAndFillsPerTool covers step 3: two providers'
// events are both folded into the shared Snapshot, and PerTool carries one
// entry per tool.
func TestRunSumsAcrossProvidersAndFillsPerTool(t *testing.T) {
	reg := provider.NewRegistry()
	mustRegister(t, reg, namedFake{id: "alpha", eventsPerSource: 2})
	mustRegister(t, reg, namedFake{id: "beta", eventsPerSource: 3})

	env := multiEnv("alpha", "beta")
	now := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)

	result, err := Run(context.Background(), reg, env, testBudget(), now, time.UTC)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}

	if len(result.PerTool) != 2 {
		t.Fatalf("len(PerTool) = %d, want 2; got %+v", len(result.PerTool), result.PerTool)
	}
	for _, id := range []string{"alpha", "beta"} {
		tr, ok := result.PerTool[id]
		if !ok {
			t.Fatalf("PerTool missing entry for %q", id)
		}
		if !tr.Available {
			t.Errorf("PerTool[%q].Available = false, want true", id)
		}
		if tr.ParseErrors != 0 {
			t.Errorf("PerTool[%q].ParseErrors = %d, want 0", id, tr.ParseErrors)
		}
	}

	wantAlpha := int64(10 + 11) // i=0,1
	wantBeta := int64(10 + 11 + 12)
	gotAlpha := sumTokens(result.Snapshot, "alpha", model.TokenInput)
	gotBeta := sumTokens(result.Snapshot, "beta", model.TokenInput)
	if gotAlpha != wantAlpha {
		t.Errorf("alpha input tokens = %d, want %d", gotAlpha, wantAlpha)
	}
	if gotBeta != wantBeta {
		t.Errorf("beta input tokens = %d, want %d", gotBeta, wantBeta)
	}
}

// TestRunCountsParseErrorWithoutBlockingOthers covers step 3: a third
// provider whose Parse always fails leaves ParseErrors == 1 for itself and
// does not prevent the other two providers' results from coming back.
func TestRunCountsParseErrorWithoutBlockingOthers(t *testing.T) {
	reg := provider.NewRegistry()
	mustRegister(t, reg, namedFake{id: "alpha", eventsPerSource: 1})
	mustRegister(t, reg, namedFake{id: "beta", eventsPerSource: 1})
	mustRegister(t, reg, namedFake{id: "gamma", failParse: true})

	env := multiEnv("alpha", "beta", "gamma")
	now := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)

	result, err := Run(context.Background(), reg, env, testBudget(), now, time.UTC)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}

	gamma := result.PerTool["gamma"]
	if gamma.ParseErrors != 1 {
		t.Errorf("gamma.ParseErrors = %d, want 1", gamma.ParseErrors)
	}
	if gamma.Available {
		t.Errorf("gamma.Available = true, want false (its only source failed to parse)")
	}

	for _, id := range []string{"alpha", "beta"} {
		tr := result.PerTool[id]
		if !tr.Available {
			t.Errorf("PerTool[%q].Available = false, want true", id)
		}
		if tr.ParseErrors != 0 {
			t.Errorf("PerTool[%q].ParseErrors = %d, want 0", id, tr.ParseErrors)
		}
	}
}

// TestRunConcurrentProvidersNoRace covers step 5: 50 concurrent fake
// providers writing into the shared Aggregator must never race. Run this
// test with -race.
func TestRunConcurrentProvidersNoRace(t *testing.T) {
	reg := provider.NewRegistry()
	ids := make([]string, 0, 50)
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("p%02d", i)
		ids = append(ids, id)
		mustRegister(t, reg, namedFake{id: id, eventsPerSource: 5})
	}

	env := multiEnv(ids...)
	now := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)

	result, err := Run(context.Background(), reg, env, testBudget(), now, time.UTC)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if len(result.PerTool) != 50 {
		t.Fatalf("len(PerTool) = %d, want 50", len(result.PerTool))
	}
}

func TestRunRejectsAlreadyDoneContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	reg := provider.NewRegistry()
	_, err := Run(ctx, reg, provider.Env{}, testBudget(), time.Now(), time.UTC)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run: error = %v, want context.Canceled", err)
	}
}

type pastDeadlineContext struct{ context.Context }

func (pastDeadlineContext) Deadline() (time.Time, bool) {
	return time.Now().Add(-time.Nanosecond), true
}

func TestRunRejectsPastDeadlineBeforeTimerDelivery(t *testing.T) {
	reg := provider.NewRegistry()
	ctx := pastDeadlineContext{Context: context.Background()}
	_, err := Run(ctx, reg, provider.Env{}, testBudget(), time.Now(), time.UTC)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run: error = %v, want context.DeadlineExceeded", err)
	}
}

func TestRunLogsCorrelatedAndRedactsPathsAboveDebug(t *testing.T) {
	reg := provider.NewRegistry()
	mustRegister(t, reg, namedFake{id: "alpha", eventsPerSource: 1})

	var logs bytes.Buffer
	old := slog.Default()
	t.Cleanup(func() { slog.SetDefault(old) })
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	ctx := WithID(context.Background(), "scan-test")
	result, err := Run(ctx, reg, fakeEnv("alpha"), testBudget(), time.Now(), time.UTC)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := logs.String()
	if strings.Contains(got, "/home/user") {
		t.Fatalf("INFO logs leaked source path: %s", got)
	}
	if n := strings.Count(got, "source read"); n != 0 {
		t.Fatalf("INFO source reads = %d, want 0", n)
	}
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		if !strings.Contains(line, "scan_id=scan-test") {
			t.Errorf("log lacks scan id: %s", line)
		}
	}
	if result.PerTool["alpha"].FilesScanned != 1 {
		t.Fatalf("files scanned = %d, want 1", result.PerTool["alpha"].FilesScanned)
	}

	logs.Reset()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	_, err = Run(ctx, reg, fakeEnv("alpha"), testBudget(), time.Now(), time.UTC)
	if err != nil {
		t.Fatalf("debug Run: %v", err)
	}
	if n := strings.Count(logs.String(), "source read"); n != 1 {
		t.Errorf("DEBUG source reads = %d, want 1; logs: %s", n, logs.String())
	}
}

func TestRunParseErrorWarnsWithoutSourceContents(t *testing.T) {
	reg := provider.NewRegistry()
	mustRegister(t, reg, namedFake{id: "alpha", failParse: true})
	var logs bytes.Buffer
	old := slog.Default()
	t.Cleanup(func() { slog.SetDefault(old) })
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	_, err := Run(WithID(context.Background(), "parse-test"), reg, fakeEnv("alpha"), testBudget(), time.Now(), time.UTC)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := logs.String()
	if !strings.Contains(got, "source parse failed") || !strings.Contains(got, "error_type=") {
		t.Errorf("missing safe parse warning: %s", got)
	}
	if strings.Contains(got, "simulated parse failure") || strings.Contains(got, "/home/user") {
		t.Errorf("parse warning leaked source content/path: %s", got)
	}
}

func TestStaleReason(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		lastEvent  time.Time
		latestFile time.Time
		want       string
	}{
		{
			name:       "provider al dia",
			lastEvent:  now.Add(-time.Hour),
			latestFile: now.Add(-30 * time.Minute),
		},
		{
			name:       "provider atrasado respecto de fuente",
			lastEvent:  now.Add(-10 * 24 * time.Hour),
			latestFile: now.Add(-time.Hour),
			want:       "source_newer_than_last_event",
		},
		{
			name:       "provider sin eventos",
			latestFile: now.Add(-time.Hour),
			want:       "no_usage_events",
		},
		{
			name:       "eventos sin nueva escritura en fuente",
			lastEvent:  now.Add(-22 * 24 * time.Hour),
			latestFile: now.Add(-22 * 24 * time.Hour),
			want:       "last_event_too_old",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := staleReason(now, tt.lastEvent, tt.latestFile); got != tt.want {
				t.Errorf("staleReason() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRunLogsStaleProviderWithoutPaths(t *testing.T) {
	reg := provider.NewRegistry()
	mustRegister(t, reg, namedFake{id: "alpha", eventsPerSource: 0})
	var logs bytes.Buffer
	old := slog.Default()
	t.Cleanup(func() { slog.SetDefault(old) })
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	_, err := Run(WithID(context.Background(), "stale-test"), reg, fakeEnv("alpha"), testBudget(), time.Now(), time.UTC)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := logs.String()
	if !strings.Contains(got, "provider usage appears stale") || !strings.Contains(got, "reason=no_usage_events") {
		t.Errorf("missing stale provider warning: %s", got)
	}
	if strings.Contains(got, "/home/user") {
		t.Errorf("stale warning leaked source path: %s", got)
	}
}

// TestRunOpenCodeCurrentGenerationAvailability exercises the real OpenCode
// provider through discovery and scan state. In particular, an empty current
// generation is still a readable source: it must become no_usage_events, not
// indistinguishable from a missing OpenCode installation.
func TestRunOpenCodeCurrentGenerationAvailability(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	assistantData := `{"time":{"created":1791804042674},"model":{"id":"fixture-model","providerID":"opencode"},"tokens":{"total":18,"input":10,"output":3,"reasoning":5,"cache":{"read":0,"write":0}}}`

	tests := []struct {
		name        string
		build       func(string) (string, error)
		available   bool
		staleReason string
		wantTokens  int64
		wantFiles   int
	}{
		{
			name: "empty current generation is available and stale",
			build: func(dir string) (string, error) {
				return testdata.BuildV2(dir,
					[]testdata.V2Session{{ID: "ses_empty", ProjectID: "prj_empty", Slug: "empty", Directory: "/redacted/empty", Version: "1"}},
					nil,
				)
			},
			available:   true,
			staleReason: "no_usage_events",
			wantFiles:   1,
		},
		{
			name: "current generation with no assistant rows is available and stale",
			build: func(dir string) (string, error) {
				return testdata.BuildV2(dir,
					[]testdata.V2Session{{ID: "ses_idle", ProjectID: "prj_idle", Slug: "idle", Directory: "/redacted/idle", Version: "1"}},
					[]testdata.V2Message{{ID: "msg_user", SessionID: "ses_idle", Type: "user", Data: `{"time":{"created":1791804042674}}`}},
				)
			},
			available:   true,
			staleReason: "no_usage_events",
			wantFiles:   1,
		},
		{
			name: "current generation with assistant rows is available and not stale",
			build: func(dir string) (string, error) {
				return testdata.BuildV2(dir,
					[]testdata.V2Session{{ID: "ses_live", ProjectID: "prj_live", Slug: "live", Directory: "/redacted/live", Version: "1"}},
					[]testdata.V2Message{{ID: "msg_assistant", SessionID: "ses_live", Type: "assistant", Data: assistantData}},
				)
			},
			available:  true,
			wantTokens: 18,
			wantFiles:  1,
		},
		{
			name: "no generation tables is unavailable and not stale",
			build: func(dir string) (string, error) {
				return testdata.BuildWithoutGeneration(dir)
			},
			wantFiles: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataHome := t.TempDir()
			if err := os.Mkdir(filepath.Join(dataHome, "opencode"), 0o755); err != nil {
				t.Fatalf("create OpenCode data directory: %v", err)
			}
			if _, err := tt.build(filepath.Join(dataHome, "opencode")); err != nil {
				t.Fatalf("build opencode fixture: %v", err)
			}

			reg := provider.NewRegistry()
			mustRegister(t, reg, opencode.New())
			result, err := Run(context.Background(), reg, provider.Env{
				GOOS: runtime.GOOS,
				Home: "/unused",
				Getenv: func(name string) string {
					if name == "OPENCODE_DATA_HOME" {
						return dataHome
					}
					return ""
				},
				FS: osRootFS{goos: runtime.GOOS},
			}, testBudget(), now, time.UTC)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			got := result.PerTool["opencode"]
			if got.FilesScanned != tt.wantFiles {
				t.Errorf("FilesScanned = %d, want %d; fixture database must be discovered", got.FilesScanned, tt.wantFiles)
			}
			if got.Available != tt.available {
				t.Errorf("Available = %t, want %t", got.Available, tt.available)
			}
			if got.StaleReason != tt.staleReason {
				t.Errorf("StaleReason = %q, want %q", got.StaleReason, tt.staleReason)
			}
			if tokens := sumTokens(result.Snapshot, "opencode", model.TokenInput) + sumTokens(result.Snapshot, "opencode", model.TokenOutput) + sumTokens(result.Snapshot, "opencode", model.TokenReasoning) + sumTokens(result.Snapshot, "opencode", model.TokenCacheRead) + sumTokens(result.Snapshot, "opencode", model.TokenCacheWrite); tokens != tt.wantTokens {
				t.Errorf("OpenCode tokens = %d, want %d", tokens, tt.wantTokens)
			}
		})
	}
}

func mustRegister(t *testing.T, reg *provider.Registry, p provider.Provider) {
	t.Helper()
	if err := reg.Register(p); err != nil {
		t.Fatalf("Register(%q): unexpected error: %v", p.Descriptor().ID, err)
	}
}

func sumTokens(s aggregate.Snapshot, tool string, class model.TokenClass) int64 {
	var total int64
	for k, v := range s.Tokens {
		if k.Tool == tool && k.Class == class && k.Window == aggregate.WindowAll {
			total += v
		}
	}
	return total
}
