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

// TestParseBasicUndoesBothNestingsWithoutInflation exercises
// rollout_basic.jsonl: three token_count turns, with the second crossing
// both nestings at once (cached_input_tokens > 0 AND
// reasoning_output_tokens > 0 in the same turn - the case where a sign
// error in either subtraction would show up immediately), a model switch
// mid-session via a second turn_context line, and two response_item tool
// calls attributed to the turn that follows them.
func TestParseBasicUndoesBothNestingsWithoutInflation(t *testing.T) {
	events := mustParse(t, filepath.Join("testdata", "rollout_basic.jsonl"))

	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}

	for _, e := range events {
		if e.Key.SessionID != "0190aaaa-0000-7000-a000-000000000001" {
			t.Errorf("SessionID = %q, want the session_meta id", e.Key.SessionID)
		}
		if _, ok := e.Tokens[model.TokenCacheWrite]; ok {
			t.Errorf("Tokens contains TokenCacheWrite = %d, want the key absent (Codex has no cache-write data)", e.Tokens[model.TokenCacheWrite])
		}
	}

	type want struct {
		messageID string
		model     string
		toolCalls int64
		input     int64
		output    int64
		cacheRead int64
		reasoning int64
	}
	wants := []want{
		{messageID: "0", model: "gpt-5.5", toolCalls: 0, input: 100, output: 50, cacheRead: 0, reasoning: 0},
		{messageID: "1", model: "gpt-5.5", toolCalls: 2, input: 110, output: 25, cacheRead: 40, reasoning: 15},
		{messageID: "2", model: "gpt-5.6-terra", toolCalls: 0, input: 150, output: 50, cacheRead: 0, reasoning: 0},
	}

	for i, e := range events {
		w := wants[i]
		if e.Key.MessageID != w.messageID {
			t.Errorf("event %d: MessageID = %q, want %q", i, e.Key.MessageID, w.messageID)
		}
		if e.Model != w.model {
			t.Errorf("event %d: Model = %q, want %q", i, e.Model, w.model)
		}
		if e.ToolCalls != w.toolCalls {
			t.Errorf("event %d: ToolCalls = %d, want %d", i, e.ToolCalls, w.toolCalls)
		}
		if got := e.Tokens[model.TokenInput]; got != w.input {
			t.Errorf("event %d: Tokens[TokenInput] = %d, want %d", i, got, w.input)
		}
		if got := e.Tokens[model.TokenOutput]; got != w.output {
			t.Errorf("event %d: Tokens[TokenOutput] = %d, want %d", i, got, w.output)
		}
		if got := e.Tokens[model.TokenCacheRead]; got != w.cacheRead {
			t.Errorf("event %d: Tokens[TokenCacheRead] = %d, want %d", i, got, w.cacheRead)
		}
		if got := e.Tokens[model.TokenReasoning]; got != w.reasoning {
			t.Errorf("event %d: Tokens[TokenReasoning] = %d, want %d", i, got, w.reasoning)
		}
	}

	// If Parse summed cumulative total_token_usage line by line instead of
	// differencing it, sumInput would be 100+250+400=750, not 360 - the
	// exact inflation bug JCB-311 shipped with, just against a schema that
	// does not exist.
	var sumInput, sumOutput, sumCached, sumReasoning int64
	for _, e := range events {
		sumInput += e.Tokens[model.TokenInput]
		sumOutput += e.Tokens[model.TokenOutput]
		sumCached += e.Tokens[model.TokenCacheRead]
		sumReasoning += e.Tokens[model.TokenReasoning]
	}
	if sumInput != 360 {
		t.Errorf("sum of Tokens[TokenInput] = %d, want 360", sumInput)
	}
	if sumOutput != 125 {
		t.Errorf("sum of Tokens[TokenOutput] = %d, want 125", sumOutput)
	}
	if sumCached != 40 {
		t.Errorf("sum of Tokens[TokenCacheRead] = %d, want 40", sumCached)
	}
	if sumReasoning != 15 {
		t.Errorf("sum of Tokens[TokenReasoning] = %d, want 15", sumReasoning)
	}

	if !events[0].Timestamp.Equal(time.Date(2026, 1, 1, 0, 0, 5, 0, time.UTC)) {
		t.Errorf("events[0].Timestamp = %v, want 2026-01-01T00:00:05Z", events[0].Timestamp)
	}
}

// TestParseTokenClassesSumToTotalDelta is the mandatory invariant test from
// JCB-321: for every emitted event, the sum of the four classes Codex can
// populate (input, output, cache_read, reasoning - cache_write is never
// populated) must equal that turn's own delta of total_tokens. This is the
// test that would have caught JCB-311's double-nesting bug, since an
// un-subtracted cache or reasoning component inflates this sum past the
// turn's actual total.
func TestParseTokenClassesSumToTotalDelta(t *testing.T) {
	tests := []struct {
		fixture        string
		wantTotalDelta []int64
	}{
		{fixture: "rollout_basic.jsonl", wantTotalDelta: []int64{150, 190, 200}},
		{fixture: "rollout_reset.jsonl", wantTotalDelta: []int64{130, 180, 100}},
		{fixture: "rollout_no_meta.jsonl", wantTotalDelta: []int64{70, 55}},
	}

	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			events := mustParse(t, filepath.Join("testdata", tt.fixture))
			if len(events) != len(tt.wantTotalDelta) {
				t.Fatalf("got %d events, want %d", len(events), len(tt.wantTotalDelta))
			}
			for i, e := range events {
				sum := e.Tokens[model.TokenInput] + e.Tokens[model.TokenOutput] +
					e.Tokens[model.TokenCacheRead] + e.Tokens[model.TokenReasoning]
				if sum != tt.wantTotalDelta[i] {
					t.Errorf("event %d: sum of the four token classes = %d, want %d (that turn's Δtotal_tokens)", i, sum, tt.wantTotalDelta[i])
				}
			}
		})
	}
}

func TestParseNoMetaUsesFilenameAsSessionID(t *testing.T) {
	path := filepath.Join("testdata", "rollout_no_meta.jsonl")
	events := mustParse(t, path)

	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}

	base := filepath.Base(path)
	want := strings.TrimSuffix(base, filepath.Ext(base))
	for i, e := range events {
		if e.Key.SessionID != want {
			t.Errorf("events[%d].Key.SessionID = %q, want %q (the filename without extension)", i, e.Key.SessionID, want)
		}
		if e.Model != "gpt-5-codex-mini" {
			t.Errorf("events[%d].Model = %q, want %q (from the only turn_context line)", i, e.Model, "gpt-5-codex-mini")
		}
	}
}

func TestParseResetNeverEmitsNegativeTokens(t *testing.T) {
	events := mustParse(t, filepath.Join("testdata", "rollout_reset.jsonl"))

	// The fixture also contains one truncated line between turn 2 and
	// turn 3: it must be skipped, not fail the whole parse.
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

	wantInputDeltas := []int64{100, 130, 75}
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
// free-text field a Codex rollout line carries that this parser does NOT
// decode (session_meta.cwd, turn_context.cwd, response_item.name,
// response_item.arguments) and asserts the sentinel never appears anywhere
// in an emitted UsageEvent. Reflection walks the whole struct instead of
// checking field by field, so this test keeps covering UsageEvent even if
// it gains a field later without anyone remembering to update this test by
// hand.
func TestParseNeverLeaksContent(t *testing.T) {
	const sentinel = "SENTINEL-PROMPT-TEXT"

	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-sentinel.jsonl")

	lines := []string{
		fmt.Sprintf(`{"timestamp":"2026-01-04T00:00:00Z","type":"session_meta","payload":{"id":"sess-sentinel","cwd":"%s"}}`, sentinel),
		fmt.Sprintf(`{"timestamp":"2026-01-04T00:00:01Z","type":"turn_context","payload":{"cwd":"%s","model":"gpt-5-codex"}}`, sentinel),
		fmt.Sprintf(`{"timestamp":"2026-01-04T00:00:02Z","type":"response_item","payload":{"type":"function_call","name":"%s","arguments":"%s"}}`, sentinel, sentinel),
		`{"timestamp":"2026-01-04T00:00:05Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"cached_input_tokens":0,"output_tokens":5,"reasoning_output_tokens":0,"total_tokens":15}}}}`,
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

// TestParseSurvivesOversizedLine is the regression test for PR #5 review
// correction 2, still live against the new schema: a line longer than
// maxLineBytes must be discarded without aborting the scan of the rest of
// the file. bufio.Scanner with Buffer(...) returns bufio.ErrTooLong on a
// line like this and Parse used to propagate scanner.Err(), silently
// dropping every turn after the oversized line - on a real 700 MB-2 GB
// rollout that is most of the session's usage.
func TestParseSurvivesOversizedLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-oversized.jsonl")

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	write := func(s string) {
		if _, err := f.WriteString(s + "\n"); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	write(`{"timestamp":"2026-01-05T00:00:00Z","type":"session_meta","payload":{"id":"sess-oversized"}}`)
	write(`{"timestamp":"2026-01-05T00:00:01Z","type":"turn_context","payload":{"model":"gpt-5-codex"}}`)
	write(`{"timestamp":"2026-01-05T00:00:05Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":0,"output_tokens":50,"reasoning_output_tokens":0,"total_tokens":150}}}}`)

	// A single line over 9 MiB: well past maxLineBytes (8 MiB), sandwiched
	// between two valid token_count lines.
	oversized := `{"timestamp":"2026-01-05T00:00:06Z","type":"response_item","payload":{"type":"message","content":"` +
		strings.Repeat("A", 9<<20) + `"}}`
	write(oversized)

	write(`{"timestamp":"2026-01-05T00:00:10Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":250,"cached_input_tokens":0,"output_tokens":90,"reasoning_output_tokens":0,"total_tokens":340}}}}`)

	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	events := mustParse(t, path)
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 (the oversized line must be skipped, not fatal to the rest of the file)", len(events))
	}
	if got := events[0].Tokens[model.TokenInput]; got != 100 {
		t.Errorf("events[0].Tokens[TokenInput] = %d, want 100", got)
	}
	if got := events[1].Tokens[model.TokenInput]; got != 150 {
		t.Errorf("events[1].Tokens[TokenInput] = %d, want 150 (the delta over event 0, proving the second valid line was reached)", got)
	}
}

// generateSyntheticRollout writes a rollout of at least targetBytes with
// monotonically increasing cumulative total_token_usage counters, so
// BenchmarkParseLarge exercises realistic delta math at scale without
// needing a checked-in multi-hundred-MB fixture.
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

	if _, err := w.WriteString(`{"timestamp":"2026-01-01T00:00:00Z","type":"session_meta","payload":{"id":"bench-session"}}` + "\n"); err != nil {
		b.Fatalf("write meta: %v", err)
	}
	if _, err := w.WriteString(`{"timestamp":"2026-01-01T00:00:00Z","type":"turn_context","payload":{"model":"gpt-5-codex"}}` + "\n"); err != nil {
		b.Fatalf("write turn_context: %v", err)
	}

	var written int64
	var input, output, cached int64
	for i := 0; written < targetBytes; i++ {
		input += 10
		output += 5
		cached++
		total := input + output
		line := fmt.Sprintf(
			`{"timestamp":"2026-01-01T00:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":%d,"cached_input_tokens":%d,"output_tokens":%d,"reasoning_output_tokens":0,"total_tokens":%d}}}}`+"\n",
			input, cached, output, total,
		)
		n, err := w.WriteString(line)
		if err != nil {
			b.Fatalf("write turn %d: %v", i, err)
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
