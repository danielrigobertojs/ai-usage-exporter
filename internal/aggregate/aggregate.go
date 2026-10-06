// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

// Package aggregate turns a stream of model.UsageEvent into the window-gauge
// snapshot /metrics serves: it dedupes by EventKey and collapses session
// identity down to per-window cardinality, never emitting a session_id.
package aggregate

import (
	"time"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
)

// TokenKey identifies one ai_usage_tokens series.
type TokenKey struct {
	Tool, Model string
	Class       model.TokenClass
	Window      Window
}

// ScopeKey identifies one tool/window scope shared by the sessions and
// tool-calls series.
type ScopeKey struct {
	Tool   string
	Window Window
}

// Snapshot is the deterministic result of aggregating a batch of events:
// every map is a fresh copy, so two Snapshot calls never alias each other's
// state and mutating one cannot affect the Aggregator or a prior snapshot.
type Snapshot struct {
	ScannedAt   time.Time
	Tokens      map[TokenKey]int64
	Sessions    map[ScopeKey]int64
	ToolCalls   map[ScopeKey]int64
	LastEventAt map[string]time.Time
	Duplicates  int
	Invalid     int
}

// Aggregator accumulates UsageEvents into window-scoped totals. It holds no
// clock and no I/O: now is fixed at construction so repeated calls to Add
// and Snapshot are deterministic.
type Aggregator struct {
	now time.Time
	tz  *time.Location

	seen map[model.EventKey]struct{}

	tokens      map[TokenKey]int64
	sessions    map[ScopeKey]map[string]struct{}
	toolCalls   map[ScopeKey]int64
	lastEventAt map[string]time.Time

	duplicates int
	invalid    int
}

// New returns an Aggregator that treats now as the scan instant and tz as
// the timezone month-to-date boundaries are computed in. A nil tz defaults
// to UTC.
func New(now time.Time, tz *time.Location) *Aggregator {
	if tz == nil {
		tz = time.UTC
	}
	return &Aggregator{
		now:         now,
		tz:          tz,
		seen:        make(map[model.EventKey]struct{}),
		tokens:      make(map[TokenKey]int64),
		sessions:    make(map[ScopeKey]map[string]struct{}),
		toolCalls:   make(map[ScopeKey]int64),
		lastEventAt: make(map[string]time.Time),
	}
}

// Add folds e into the running aggregation. It returns false, without
// changing any totals, when e is invalid (Invalid is incremented) or when
// its EventKey has already been seen (Duplicates is incremented) - the
// latter is what keeps compaction, /resume, and session forks from
// inflating totals when the same message reappears in the logs.
func (a *Aggregator) Add(e model.UsageEvent) bool {
	if err := e.Valid(); err != nil {
		a.invalid++
		return false
	}

	if _, dup := a.seen[e.Key]; dup {
		a.duplicates++
		return false
	}
	a.seen[e.Key] = struct{}{}

	windows := Windows(e.Timestamp, a.now, a.tz)
	for _, w := range windows {
		for class, count := range e.Tokens {
			if count == 0 {
				continue
			}
			key := TokenKey{Tool: e.Tool, Model: e.Model, Class: class, Window: w}
			a.tokens[key] += count
		}

		scope := ScopeKey{Tool: e.Tool, Window: w}
		if e.Key.SessionID != "" {
			set, ok := a.sessions[scope]
			if !ok {
				set = make(map[string]struct{})
				a.sessions[scope] = set
			}
			set[e.Key.SessionID] = struct{}{}
		}

		if e.ToolCalls != 0 {
			a.toolCalls[scope] += e.ToolCalls
		}
	}

	if last, ok := a.lastEventAt[e.Tool]; !ok || e.Timestamp.After(last) {
		a.lastEventAt[e.Tool] = e.Timestamp
	}

	return true
}

// Snapshot returns the current aggregation as independent map copies.
func (a *Aggregator) Snapshot() Snapshot {
	tokens := make(map[TokenKey]int64, len(a.tokens))
	for k, v := range a.tokens {
		tokens[k] = v
	}

	sessions := make(map[ScopeKey]int64, len(a.sessions))
	for k, set := range a.sessions {
		sessions[k] = int64(len(set))
	}

	toolCalls := make(map[ScopeKey]int64, len(a.toolCalls))
	for k, v := range a.toolCalls {
		toolCalls[k] = v
	}

	lastEventAt := make(map[string]time.Time, len(a.lastEventAt))
	for k, v := range a.lastEventAt {
		lastEventAt[k] = v
	}

	return Snapshot{
		ScannedAt:   a.now,
		Tokens:      tokens,
		Sessions:    sessions,
		ToolCalls:   toolCalls,
		LastEventAt: lastEventAt,
		Duplicates:  a.duplicates,
		Invalid:     a.invalid,
	}
}
