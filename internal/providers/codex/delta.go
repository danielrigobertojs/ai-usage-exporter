// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package codex

// Cumulative holds the last_* counters exactly as one turn line in a Codex
// rollout reports them: the running total for the session's current context
// window, not a per-turn delta. Summing these across turns the way a naive
// parser would double (and triple, and quadruple...) count every token,
// which is the trap this package exists to avoid.
type Cumulative struct {
	Input  int64
	Output int64
	Cached int64
	Total  int64
}

// Tracker converts a sequence of Cumulative observations from one rollout
// into per-turn deltas. It is scoped to a single session: codex.go creates
// one Tracker per Source it parses.
type Tracker struct {
	prev    Cumulative
	hasPrev bool
}

// NewTracker returns a Tracker with no prior observation.
func NewTracker() *Tracker {
	return &Tracker{}
}

// Delta returns the increment of c over the previous observation passed to
// this Tracker. The first call on a new Tracker has nothing to diff against,
// so the entire observation is the delta. When a field decreases relative to
// the prior observation - a compaction or a fresh context window - that
// field's counter restarted at zero from Codex's point of view, so Delta
// reports the field's current absolute value rather than a negative number.
func (t *Tracker) Delta(c Cumulative) Cumulative {
	if !t.hasPrev {
		t.hasPrev = true
		t.prev = c
		return c
	}

	d := Cumulative{
		Input:  deltaField(t.prev.Input, c.Input),
		Output: deltaField(t.prev.Output, c.Output),
		Cached: deltaField(t.prev.Cached, c.Cached),
		Total:  deltaField(t.prev.Total, c.Total),
	}
	t.prev = c
	return d
}

// deltaField reports the increment from prev to cur, or cur itself if cur
// dropped below prev (a counter reset) - never a negative number.
func deltaField(prev, cur int64) int64 {
	if cur < prev {
		return cur
	}
	return cur - prev
}
