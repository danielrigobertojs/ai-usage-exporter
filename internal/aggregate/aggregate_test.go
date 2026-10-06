// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package aggregate

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
)

func TestAggregatorDedupeByEventKey(t *testing.T) {
	now := time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC)
	a := New(now, time.UTC)

	key := model.EventKey{Tool: "claude-code", SessionID: "s1", MessageID: "m1"}
	first := model.UsageEvent{
		Key:       key,
		Tool:      "claude-code",
		Model:     "claude-opus-4",
		Timestamp: now,
		Tokens:    map[model.TokenClass]int64{model.TokenInput: 10},
	}
	second := model.UsageEvent{
		Key:       key,
		Tool:      "claude-code",
		Model:     "claude-opus-4",
		Timestamp: now,
		Tokens:    map[model.TokenClass]int64{model.TokenInput: 999},
	}

	if ok := a.Add(first); !ok {
		t.Fatalf("Add(first) = false, want true")
	}
	if ok := a.Add(second); ok {
		t.Fatalf("Add(second) = true, want false (duplicate EventKey)")
	}

	snap := a.Snapshot()
	if snap.Duplicates != 1 {
		t.Errorf("Duplicates = %d, want 1", snap.Duplicates)
	}

	tokenKey := TokenKey{Tool: "claude-code", Model: "claude-opus-4", Class: model.TokenInput, Window: WindowAll}
	if got := snap.Tokens[tokenKey]; got != 10 {
		t.Errorf("Tokens[%v] = %d, want 10 (only the first event's tokens)", tokenKey, got)
	}
}

func TestAggregatorInvalidEvent(t *testing.T) {
	now := time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC)
	a := New(now, time.UTC)

	invalid := model.UsageEvent{
		Tool:      "codex-cli",
		Timestamp: now,
		Tokens:    map[model.TokenClass]int64{model.TokenInput: -5},
	}

	if ok := a.Add(invalid); ok {
		t.Fatalf("Add(invalid) = true, want false")
	}

	snap := a.Snapshot()
	if snap.Invalid != 1 {
		t.Errorf("Invalid = %d, want 1", snap.Invalid)
	}
	if snap.Duplicates != 0 {
		t.Errorf("Duplicates = %d, want 0", snap.Duplicates)
	}
}

func TestAggregatorSessionCardinality(t *testing.T) {
	now := time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC)
	a := New(now, time.UTC)

	// Three events in session s1, all within 24h: counts as one session.
	for i, ts := range []time.Time{now, now.Add(-1 * time.Hour), now.Add(-2 * time.Hour)} {
		e := model.UsageEvent{
			Key:       model.EventKey{Tool: "claude-code", SessionID: "s1", MessageID: fmt.Sprintf("m%d", i)},
			Tool:      "claude-code",
			Timestamp: ts,
		}
		if ok := a.Add(e); !ok {
			t.Fatalf("Add(s1 event %d) = false, want true", i)
		}
	}

	// A second, distinct session, also within 24h.
	s2 := model.UsageEvent{
		Key:       model.EventKey{Tool: "claude-code", SessionID: "s2", MessageID: "m0"},
		Tool:      "claude-code",
		Timestamp: now,
	}
	if ok := a.Add(s2); !ok {
		t.Fatalf("Add(s2) = false, want true")
	}

	// A third session whose only event is outside 24h (but within 30d and
	// this month), so it must not count toward the 24h scope.
	s3 := model.UsageEvent{
		Key:       model.EventKey{Tool: "claude-code", SessionID: "s3", MessageID: "m0"},
		Tool:      "claude-code",
		Timestamp: now.Add(-10 * 24 * time.Hour),
	}
	if ok := a.Add(s3); !ok {
		t.Fatalf("Add(s3) = false, want true")
	}

	snap := a.Snapshot()

	if got := snap.Sessions[ScopeKey{Tool: "claude-code", Window: Window24h}]; got != 2 {
		t.Errorf("Sessions[24h] = %d, want 2 (s1 and s2, not s3)", got)
	}
	if got := snap.Sessions[ScopeKey{Tool: "claude-code", Window: WindowAll}]; got != 3 {
		t.Errorf("Sessions[all] = %d, want 3 (s1, s2 and s3)", got)
	}
}

func TestSnapshotIsDeterministic(t *testing.T) {
	now := time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC)
	a := New(now, time.UTC)

	a.Add(model.UsageEvent{
		Key:       model.EventKey{Tool: "claude-code", SessionID: "s1", MessageID: "m1"},
		Tool:      "claude-code",
		Model:     "claude-opus-4",
		Timestamp: now,
		Tokens:    map[model.TokenClass]int64{model.TokenInput: 42},
		ToolCalls: 3,
	})

	first := a.Snapshot()
	second := a.Snapshot()

	if !reflect.DeepEqual(first, second) {
		t.Errorf("Snapshot() is not deterministic: first = %+v, second = %+v", first, second)
	}

	// Mutating the map returned by one snapshot must not affect the other
	// or the Aggregator's internal state.
	for k := range first.Tokens {
		first.Tokens[k] = -1
		break
	}
	third := a.Snapshot()
	if !reflect.DeepEqual(second, third) {
		t.Errorf("Snapshot() maps alias internal state: second = %+v, third = %+v", second, third)
	}
}

// TestAggregatorIdempotentReplay is the property test from the ticket: feed
// 10,000 random-but-valid events with unique keys once, snapshot, then feed
// the exact same batch again. The second pass must duplicate every event
// (Duplicates == 10000) while leaving every aggregated total unchanged.
func TestAggregatorIdempotentReplay(t *testing.T) {
	const n = 10000
	rng := rand.New(rand.NewSource(42))

	tools := []string{"claude-code", "codex-cli", "opencode"}
	models := []string{"claude-opus-4", "gpt-5-codex", "gpt-5"}
	classes := []model.TokenClass{model.TokenInput, model.TokenOutput, model.TokenCacheRead, model.TokenCacheWrite, model.TokenReasoning}

	now := time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC)

	events := make([]model.UsageEvent, n)
	for i := 0; i < n; i++ {
		tool := tools[rng.Intn(len(tools))]
		tokens := make(map[model.TokenClass]int64)
		for _, c := range classes {
			if rng.Intn(2) == 0 {
				tokens[c] = rng.Int63n(1000)
			}
		}
		events[i] = model.UsageEvent{
			Key: model.EventKey{
				Tool:      tool,
				SessionID: fmt.Sprintf("session-%d", rng.Intn(500)),
				MessageID: fmt.Sprintf("msg-%d", i), // unique, guarantees no in-batch duplicates
			},
			Tool:      tool,
			Model:     models[rng.Intn(len(models))],
			Timestamp: now.Add(-time.Duration(rng.Int63n(int64(45 * 24 * time.Hour)))),
			Tokens:    tokens,
			ToolCalls: rng.Int63n(5),
		}
	}

	a := New(now, time.UTC)

	for _, e := range events {
		if ok := a.Add(e); !ok {
			t.Fatalf("first pass: Add(%+v) = false, want true (keys are unique by construction)", e.Key)
		}
	}
	firstPass := a.Snapshot()
	if firstPass.Duplicates != 0 {
		t.Fatalf("first pass Duplicates = %d, want 0", firstPass.Duplicates)
	}

	for _, e := range events {
		if ok := a.Add(e); ok {
			t.Fatalf("second pass: Add(%+v) = true, want false (already seen)", e.Key)
		}
	}
	secondPass := a.Snapshot()

	if secondPass.Duplicates != n {
		t.Errorf("Duplicates after replay = %d, want %d", secondPass.Duplicates, n)
	}
	if !reflect.DeepEqual(firstPass.Tokens, secondPass.Tokens) {
		t.Errorf("Tokens changed after a pure replay of already-seen events")
	}
	if !reflect.DeepEqual(firstPass.Sessions, secondPass.Sessions) {
		t.Errorf("Sessions changed after a pure replay of already-seen events")
	}
	if !reflect.DeepEqual(firstPass.ToolCalls, secondPass.ToolCalls) {
		t.Errorf("ToolCalls changed after a pure replay of already-seen events")
	}
	if !reflect.DeepEqual(firstPass.LastEventAt, secondPass.LastEventAt) {
		t.Errorf("LastEventAt changed after a pure replay of already-seen events")
	}
}
