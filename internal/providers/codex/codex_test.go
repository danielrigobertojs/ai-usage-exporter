// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package codex

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider"
)

func mustParse(t *testing.T, path string) []model.UsageEvent {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	src := provider.Source{Path: path, Kind: provider.SourceJSONL, Size: info.Size(), ModTime: info.ModTime()}

	var events []model.UsageEvent
	err = New().Parse(context.Background(), src, func(e model.UsageEvent) error {
		events = append(events, e)
		return nil
	})
	if err != nil {
		t.Fatalf("Parse(%s): %v", path, err)
	}
	return events
}

func TestDescriptorValid(t *testing.T) {
	d := New().Descriptor()
	if err := d.Validate(); err != nil {
		t.Fatalf("Descriptor().Validate() = %v, want nil", err)
	}
	if d.ID != "codex" {
		t.Errorf("Descriptor().ID = %q, want %q", d.ID, "codex")
	}
}

func TestParseBasicReconstructsTotalsWithoutInflation(t *testing.T) {
	events := mustParse(t, filepath.Join("testdata", "rollout_basic.jsonl"))

	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}

	for _, e := range events {
		if e.Key.SessionID != "sess-basic-001" {
			t.Errorf("SessionID = %q, want %q (from session_meta)", e.Key.SessionID, "sess-basic-001")
		}
		if e.Model != "gpt-5-codex" {
			t.Errorf("Model = %q, want %q", e.Model, "gpt-5-codex")
		}
	}

	var sumInput, sumOutput, sumCached int64
	for _, e := range events {
		sumInput += e.Tokens[model.TokenInput]
		sumOutput += e.Tokens[model.TokenOutput]
		sumCached += e.Tokens[model.TokenCacheRead]
	}

	// The fixture's last turn reports last_input_tokens=400 as the
	// cumulative total. If Parse summed cumulative counters line by line
	// instead of differencing them, this sum would be 100+250+400=750, not
	// 400 - the exact inflation bug this package exists to avoid.
	if sumInput != 400 {
		t.Errorf("sum of Tokens[TokenInput] = %d, want 400 (the last turn's cumulative last_input_tokens)", sumInput)
	}
	if sumOutput != 90 {
		t.Errorf("sum of Tokens[TokenOutput] = %d, want 90", sumOutput)
	}
	if sumCached != 15 {
		t.Errorf("sum of Tokens[TokenCacheRead] = %d, want 15", sumCached)
	}

	wantDeltas := []int64{100, 150, 150}
	for i, e := range events {
		if got := e.Tokens[model.TokenInput]; got != wantDeltas[i] {
			t.Errorf("event %d: Tokens[TokenInput] = %d, want %d", i, got, wantDeltas[i])
		}
	}

	if !events[0].Timestamp.Equal(time.Date(2026, 1, 1, 0, 0, 5, 0, time.UTC)) {
		t.Errorf("events[0].Timestamp = %v, want 2026-01-01T00:00:05Z", events[0].Timestamp)
	}
	if events[0].Key.MessageID != "turn-1" {
		t.Errorf("events[0].Key.MessageID = %q, want %q", events[0].Key.MessageID, "turn-1")
	}
}

func TestParseNoMetaUsesFilenameAsSessionID(t *testing.T) {
	path := filepath.Join("testdata", "rollout_no_meta.jsonl")
	events := mustParse(t, path)

	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}

	want := filepath.Base(path)
	for i, e := range events {
		if e.Key.SessionID != want {
			t.Errorf("events[%d].Key.SessionID = %q, want %q (the filename)", i, e.Key.SessionID, want)
		}
	}
}

func TestParseResetNeverEmitsNegativeTokens(t *testing.T) {
	events := mustParse(t, filepath.Join("testdata", "rollout_reset.jsonl"))

	// The fixture also contains one truncated line between turn-2 and
	// turn-3: it must be skipped, not fail the whole parse.
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3 (truncated line must be skipped, not fatal)", len(events))
	}

	for i, e := range events {
		for class, count := range e.Tokens {
			if count < 0 {
				t.Errorf("event %d: Tokens[%s] = %d, want >= 0", i, class, count)
			}
		}
		if err := e.Valid(); err != nil {
			t.Errorf("event %d: Valid() = %v, want nil", i, err)
		}
	}

	wantInputDeltas := []int64{100, 150, 80}
	for i, e := range events {
		if got := e.Tokens[model.TokenInput]; got != wantInputDeltas[i] {
			t.Errorf("event %d: Tokens[TokenInput] = %d, want %d", i, got, wantInputDeltas[i])
		}
	}
}

func TestParseContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	path := filepath.Join("testdata", "rollout_basic.jsonl")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	src := provider.Source{Path: path, Kind: provider.SourceJSONL, Size: info.Size(), ModTime: info.ModTime()}

	called := false
	err = New().Parse(ctx, src, func(model.UsageEvent) error {
		called = true
		return nil
	})
	if err != context.Canceled {
		t.Fatalf("Parse() error = %v, want context.Canceled", err)
	}
	if called {
		t.Errorf("emit was called on an already-canceled context")
	}
}

// TestParseNeverLeaksContent injects SENTINEL-PROMPT-TEXT into every
// free-text field a Codex rollout line carries (cwd, and a content-bearing
// field on the turn payload) and asserts the sentinel never appears
// anywhere in an emitted UsageEvent. Reflection walks the whole struct
// instead of checking field by field, so this test keeps covering
// UsageEvent even if it gains a field later without anyone remembering to
// update this test by hand.
func TestParseNeverLeaksContent(t *testing.T) {
	const sentinel = "SENTINEL-PROMPT-TEXT"

	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-sentinel.jsonl")

	lines := []string{
		fmt.Sprintf(`{"type":"session_meta","timestamp":"2026-01-04T00:00:00Z","payload":{"id":"sess-sentinel","cwd":"%s"}}`, sentinel),
		fmt.Sprintf(`{"type":"turn","timestamp":"2026-01-04T00:00:05Z","payload":{"id":"turn-1","model":"gpt-5-codex","last_input_tokens":10,"last_output_tokens":5,"last_cached_tokens":0,"last_total_tokens":15,"text":"%s"}}`, sentinel),
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	events := mustParse(t, path)
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}

	for _, e := range events {
		assertNoSentinel(t, reflect.ValueOf(e), sentinel)
	}
}

func assertNoSentinel(t *testing.T, v reflect.Value, sentinel string) {
	t.Helper()
	switch v.Kind() {
	case reflect.String:
		if strings.Contains(v.String(), sentinel) {
			t.Errorf("sentinel leaked into a %s field: %q", v.Type(), v.String())
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			assertNoSentinel(t, v.Field(i), sentinel)
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			assertNoSentinel(t, iter.Key(), sentinel)
			assertNoSentinel(t, iter.Value(), sentinel)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			assertNoSentinel(t, v.Index(i), sentinel)
		}
	case reflect.Ptr, reflect.Interface:
		if !v.IsNil() {
			assertNoSentinel(t, v.Elem(), sentinel)
		}
	}
}

// generateSyntheticRollout writes a rollout of at least targetBytes with
// monotonically increasing cumulative counters, so BenchmarkParseLarge
// exercises realistic delta math at scale without needing a checked-in
// multi-hundred-MB fixture.
func generateSyntheticRollout(b *testing.B, targetBytes int64) string {
	b.Helper()
	path := filepath.Join(b.TempDir(), "rollout-bench.jsonl")

	f, err := os.Create(path)
	if err != nil {
		b.Fatalf("create: %v", err)
	}
	defer f.Close()

	w := bufio.NewWriterSize(f, 1<<20)
	defer w.Flush()

	if _, err := w.WriteString(`{"type":"session_meta","timestamp":"2026-01-01T00:00:00Z","payload":{"id":"bench-session","cwd":"REDACTED"}}` + "\n"); err != nil {
		b.Fatalf("write meta: %v", err)
	}

	var written int64
	var input, output, cached int64
	for i := 0; written < targetBytes; i++ {
		input += 10
		output += 5
		cached++
		total := input + output
		line := fmt.Sprintf(
			`{"type":"turn","timestamp":"2026-01-01T00:00:00Z","payload":{"id":"turn-%d","model":"gpt-5-codex","last_input_tokens":%d,"last_output_tokens":%d,"last_cached_tokens":%d,"last_total_tokens":%d}}`+"\n",
			i, input, output, cached, total,
		)
		n, err := w.WriteString(line)
		if err != nil {
			b.Fatalf("write turn: %v", err)
		}
		written += int64(n)
	}

	if err := w.Flush(); err != nil {
		b.Fatalf("flush: %v", err)
	}
	return path
}

// BenchmarkParseLarge proves Parse streams a rollout instead of loading it:
// it must finish a 200 MB synthetic file in under 3s and, critically, must
// not leave more than 64 MiB of *live* heap behind once it returns. B/op
// from -benchmem alone can't tell streaming apart from "allocate it all and
// let the GC clean up after": encoding/json's per-line churn shows up there
// too and gets collected regardless. Comparing runtime.MemStats.HeapAlloc
// before and after a GC-settled Parse call measures what actually stayed
// resident, which is the property that matters for a tool meant to run
// against real 700 MB-2 GB session logs.
func BenchmarkParseLarge(b *testing.B) {
	const maxHeapGrowth = 64 << 20
	const maxDuration = 3 * time.Second

	path := generateSyntheticRollout(b, 200<<20)
	info, err := os.Stat(path)
	if err != nil {
		b.Fatalf("stat: %v", err)
	}
	src := provider.Source{Path: path, Kind: provider.SourceJSONL, Size: info.Size(), ModTime: info.ModTime()}
	p := New()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)

		start := time.Now()
		count := 0
		err := p.Parse(context.Background(), src, func(model.UsageEvent) error {
			count++
			return nil
		})
		elapsed := time.Since(start)
		if err != nil {
			b.Fatalf("Parse: %v", err)
		}
		if elapsed > maxDuration {
			b.Fatalf("Parse took %s for a 200 MB rollout, want < %s", elapsed, maxDuration)
		}

		runtime.GC()
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		if after.HeapAlloc > before.HeapAlloc {
			if grown := after.HeapAlloc - before.HeapAlloc; grown > maxHeapGrowth {
				b.Fatalf("live heap grew by %d bytes parsing a 200 MB rollout, want < %d (Parse is not streaming)", grown, maxHeapGrowth)
			}
		}
	}
}
