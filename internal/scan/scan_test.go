// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package scan

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/aggregate"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider"
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
