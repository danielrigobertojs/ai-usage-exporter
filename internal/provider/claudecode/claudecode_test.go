// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package claudecode

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/aggregate"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider"
)

func parseFile(t *testing.T, path string) []model.UsageEvent {
	t.Helper()
	var got []model.UsageEvent
	err := New().Parse(context.Background(), provider.Source{Path: path}, func(e model.UsageEvent) error {
		got = append(got, e)
		return nil
	})
	if err != nil {
		t.Fatalf("Parse(%s): unexpected error: %v", path, err)
	}
	return got
}

func TestDescriptorIsValid(t *testing.T) {
	d := New().Descriptor()
	if err := d.Validate(); err != nil {
		t.Fatalf("Descriptor().Validate(): %v", err)
	}
	if d.ID != "claude-code" {
		t.Errorf("Descriptor().ID = %q, want %q", d.ID, "claude-code")
	}
	if d.DisplayName != "Claude Code" {
		t.Errorf("Descriptor().DisplayName = %q, want %q", d.DisplayName, "Claude Code")
	}
	if d.Kind != provider.SourceJSONL {
		t.Errorf("Descriptor().Kind = %q, want %q", d.Kind, provider.SourceJSONL)
	}
	if want := []string{"CLAUDE_CONFIG_DIR"}; !reflect.DeepEqual(d.HomeEnv, want) {
		t.Errorf("Descriptor().HomeEnv = %v, want %v", d.HomeEnv, want)
	}
	wantCaps := provider.Capabilities{HasTokens: true, HasCacheTokens: true, HasToolCalls: true, HasNativeCost: false}
	if d.Capabilities != wantCaps {
		t.Errorf("Descriptor().Capabilities = %+v, want %+v", d.Capabilities, wantCaps)
	}
	if len(d.Roots) != 1 || d.Roots[0].Base != provider.BaseHome || d.Roots[0].Rel != ".claude/projects" || d.Roots[0].Glob != "*/*.jsonl" {
		t.Errorf("Descriptor().Roots = %+v, want a single {BaseHome, \".claude/projects\", \"*/*.jsonl\"}", d.Roots)
	}
}

func TestParseBasicFixtureEmitsOnlyAssistantEvents(t *testing.T) {
	events := parseFile(t, filepath.Join("testdata", "session_basic.jsonl"))

	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}

	want0 := model.UsageEvent{
		Key: model.EventKey{
			Tool:      "claude-code",
			SessionID: "session-basic-0001",
			MessageID: "11111111-0000-0000-0000-000000000002",
		},
		Tool:      "claude-code",
		Model:     "claude-opus-4",
		ProjectID: "/home/user/project-a",
		Role:      "assistant",
		Timestamp: time.Date(2026, 1, 10, 9, 0, 5, 0, time.UTC),
		Tokens: map[model.TokenClass]int64{
			model.TokenInput:      120,
			model.TokenOutput:     45,
			model.TokenCacheRead:  0,
			model.TokenCacheWrite: 0,
		},
		ToolCalls: 0,
	}
	assertEventEqual(t, events[0], want0)

	want1 := model.UsageEvent{
		Key: model.EventKey{
			Tool:      "claude-code",
			SessionID: "session-basic-0001",
			MessageID: "11111111-0000-0000-0000-000000000003",
		},
		Tool:      "claude-code",
		Model:     "claude-sonnet-5",
		ProjectID: "/home/user/project-a",
		Role:      "assistant",
		Timestamp: time.Date(2026, 1, 10, 9, 0, 12, 500000000, time.UTC),
		Tokens: map[model.TokenClass]int64{
			model.TokenInput:      300,
			model.TokenOutput:     80,
			model.TokenCacheRead:  500,
			model.TokenCacheWrite: 200,
		},
		ToolCalls: 2,
	}
	assertEventEqual(t, events[1], want1)

	for i, e := range events {
		if err := e.Valid(); err != nil {
			t.Errorf("events[%d].Valid(): %v", i, err)
		}
	}
}

func assertEventEqual(t *testing.T, got, want model.UsageEvent) {
	t.Helper()
	if got.Key != want.Key {
		t.Errorf("Key = %+v, want %+v", got.Key, want.Key)
	}
	if got.Tool != want.Tool {
		t.Errorf("Tool = %q, want %q", got.Tool, want.Tool)
	}
	if got.Model != want.Model {
		t.Errorf("Model = %q, want %q", got.Model, want.Model)
	}
	if got.ProjectID != want.ProjectID {
		t.Errorf("ProjectID = %q, want %q", got.ProjectID, want.ProjectID)
	}
	if got.Role != want.Role {
		t.Errorf("Role = %q, want %q", got.Role, want.Role)
	}
	if !got.Timestamp.Equal(want.Timestamp) {
		t.Errorf("Timestamp = %v, want %v", got.Timestamp, want.Timestamp)
	}
	if got.Timestamp.Location() != time.UTC {
		t.Errorf("Timestamp location = %v, want UTC", got.Timestamp.Location())
	}
	if !reflect.DeepEqual(got.Tokens, want.Tokens) {
		t.Errorf("Tokens = %+v, want %+v", got.Tokens, want.Tokens)
	}
	if got.ToolCalls != want.ToolCalls {
		t.Errorf("ToolCalls = %d, want %d", got.ToolCalls, want.ToolCalls)
	}
}

func TestParseCompactedFixtureDedupesInAggregatorNotInProvider(t *testing.T) {
	events := parseFile(t, filepath.Join("testdata", "session_compacted.jsonl"))

	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2 (the provider must not dedupe on its own)", len(events))
	}
	if events[0].Key != events[1].Key {
		t.Fatalf("events have different EventKeys: %+v != %+v", events[0].Key, events[1].Key)
	}

	agg := aggregate.New(time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC), nil)
	if ok := agg.Add(events[0]); !ok {
		t.Error("Add(events[0]) = false, want true")
	}
	if ok := agg.Add(events[1]); ok {
		t.Error("Add(events[1]) = true, want false (duplicate EventKey)")
	}
	if got := agg.Snapshot().Duplicates; got != 1 {
		t.Errorf("Snapshot().Duplicates = %d, want 1", got)
	}
}

func TestParseMalformedFixtureSkipsBadLinesWithoutError(t *testing.T) {
	events := parseFile(t, filepath.Join("testdata", "session_malformed.jsonl"))

	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	if got, want := events[0].Key.MessageID, "33333333-0000-0000-0000-000000000004"; got != want {
		t.Errorf("events[0].Key.MessageID = %q, want %q", got, want)
	}
	if got, want := events[0].Tokens[model.TokenInput], int64(77); got != want {
		t.Errorf("events[0].Tokens[input] = %d, want %d", got, want)
	}
}

func TestParseEmptyFileEmitsNoEventsNoError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.jsonl")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	events := parseFile(t, path)
	if len(events) != 0 {
		t.Fatalf("len(events) = %d, want 0", len(events))
	}
}

func TestParseSkipsOversizedLineWithoutAbortingFile(t *testing.T) {
	oversized := bytes.Repeat([]byte("x"), provider.MaxJSONLLineBytes+(1<<20)) // 1 MiB over the cap

	valid := `{"type":"assistant","uuid":"oversized-ok","sessionId":"s","cwd":"/home/user/p","timestamp":"2026-04-01T00:00:00Z","message":{"model":"claude-opus-4","content":[],"usage":{"input_tokens":1,"output_tokens":1,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`

	var buf bytes.Buffer
	buf.Write(oversized)
	buf.WriteByte('\n')
	buf.WriteString(valid)
	buf.WriteByte('\n')

	path := filepath.Join(t.TempDir(), "oversized.jsonl")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	events := parseFile(t, path)
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1 (the oversized line must be skipped, not fatal)", len(events))
	}
	if got, want := events[0].Key.MessageID, "oversized-ok"; got != want {
		t.Errorf("events[0].Key.MessageID = %q, want %q", got, want)
	}
}

func TestParseReturnsErrorWhenSourceCannotBeOpened(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.jsonl")
	err := New().Parse(context.Background(), provider.Source{Path: path}, func(model.UsageEvent) error {
		return nil
	})
	if err == nil {
		t.Fatal("Parse: want error for a missing source file, got nil")
	}
	if strings.Contains(err.Error(), "SENTINEL-PROMPT-TEXT") || strings.Contains(err.Error(), path) {
		t.Errorf("Parse error leaked source path: %q", err)
	}
}

func TestProjectIDFallsBackToSessionFileParentDirWhenCWDMissing(t *testing.T) {
	path := filepath.FromSlash("/home/user/.claude/projects/my-project-slug/session.jsonl")
	if got, want := projectID("", path), "my-project-slug"; got != want {
		t.Errorf("projectID(\"\", %q) = %q, want %q", path, got, want)
	}
}

// singleErrReader returns data together with a non-EOF error on its first
// Read call, the way a failing disk read can - bufio.Reader must surface
// that error once the data ahead of it has been consumed.
type singleErrReader struct {
	data []byte
	err  error
	done bool
}

func (r *singleErrReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return copy(p, r.data), r.err
}

func TestNextLinePropagatesGenuineReadError(t *testing.T) {
	wantErr := errors.New("boom")
	br := bufio.NewReader(&singleErrReader{data: []byte("incomplete"), err: wantErr})

	line, err := provider.ReadJSONLLine(br)
	if line != nil {
		t.Errorf("line = %q, want nil", line)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestParsePropagatesEmitError(t *testing.T) {
	wantErr := errors.New("boom")
	calls := 0
	err := New().Parse(context.Background(), provider.Source{Path: filepath.Join("testdata", "session_basic.jsonl")}, func(model.UsageEvent) error {
		calls++
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Parse: error = %v, want %v", err, wantErr)
	}
	if calls != 1 {
		t.Errorf("emit called %d times, want 1 (Parse must stop on first error)", calls)
	}
}

func TestParseRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := 0
	err := New().Parse(ctx, provider.Source{Path: filepath.Join("testdata", "session_basic.jsonl")}, func(model.UsageEvent) error {
		calls++
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Parse: error = %v, want context.Canceled", err)
	}
	if calls != 0 {
		t.Errorf("emit called %d times, want 0 (Parse must stop before emitting)", calls)
	}
}

// TestParseNeverEmitsFixtureContent is the mandatory sentinel test: every
// content field in the fixtures is replaced with a marker that must never
// survive into a UsageEvent. It walks every emitted event by reflection
// rather than field-by-field, so it keeps catching this the day
// model.UsageEvent grows a new field - see JCB-310's acceptance criteria.
func TestParseNeverEmitsFixtureContent(t *testing.T) {
	const sentinel = "SENTINEL-PROMPT-TEXT"

	fixtures := []string{
		filepath.Join("testdata", "session_basic.jsonl"),
		filepath.Join("testdata", "session_compacted.jsonl"),
		filepath.Join("testdata", "session_malformed.jsonl"),
	}

	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			raw, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatalf("ReadFile(%s): %v", fixture, err)
			}
			poisoned := bytes.ReplaceAll(raw, []byte("REDACTED"), []byte(sentinel))

			path := filepath.Join(t.TempDir(), "poisoned.jsonl")
			if err := os.WriteFile(path, poisoned, 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			events := parseFile(t, path)
			for i, e := range events {
				assertNoSentinel(t, reflect.ValueOf(e), sentinel, fmt.Sprintf("events[%d]", i))
			}
		})
	}
}

// assertNoSentinel walks v (a model.UsageEvent, by value) through every
// string it contains - struct fields, map keys and values, slice elements -
// and fails t if any of them contains sentinel.
func assertNoSentinel(t *testing.T, v reflect.Value, sentinel, path string) {
	t.Helper()
	switch v.Kind() {
	case reflect.String:
		if strings.Contains(v.String(), sentinel) {
			t.Errorf("%s = %q contains sentinel content %q", path, v.String(), sentinel)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			assertNoSentinel(t, v.Field(i), sentinel, path+"."+v.Type().Field(i).Name)
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			assertNoSentinel(t, k, sentinel, path+"[key]")
			assertNoSentinel(t, v.MapIndex(k), sentinel, path+"[value]")
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			assertNoSentinel(t, v.Index(i), sentinel, fmt.Sprintf("%s[%d]", path, i))
		}
	case reflect.Ptr, reflect.Interface:
		if !v.IsNil() {
			assertNoSentinel(t, v.Elem(), sentinel, path)
		}
	}
}
