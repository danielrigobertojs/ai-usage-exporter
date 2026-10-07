// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package codex

// Cumulative holds the total_token_usage counters exactly as one
// event_msg/token_count line in a Codex rollout reports them: the running
// total for the whole session as of that turn, not a per-turn delta.
// Summing these across turns the way a naive parser would double count
// every token, which is the trap this package exists to avoid.
//
// CachedInput and ReasoningOutput are themselves nested inside Input and
// Output respectively (OpenAI's prompt-caching and reasoning-token
// convention) - see docs/providers/codex.md for the evidence. Tracker only
// undoes the cumulative-vs-delta problem; undoing the nesting happens in
// codex.go after Delta returns.
type Cumulative struct {
	Input           int64
	CachedInput     int64
	Output          int64
	ReasoningOutput int64
	Total           int64
}

// Tracker converts a sequence of Cumulative observations from one rollout
// into per-turn deltas. It is scoped to a single session: codex.go creates
// one Tracker per Source it parses, fed from total_token_usage (the
// session-wide running total), never last_token_usage (which repeats
// verbatim on duplicate token_count emissions and would double-count them
// if treated as a delta).
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
// so the entire observation is the delta. When a field in a later
// observation is lower than the previous one - a compaction or a fresh
// context window - that field's counter restarted at zero from Codex's
// point of view, so Delta reports the field's current absolute value rather
// than a negative number. A duplicate observation (identical to the
// previous one) yields an all-zero delta, which is what makes Codex's
// duplicate token_count emissions a no-op instead of double-counted.
func (t *Tracker) Delta(c Cumulative) Cumulative {
	if !t.hasPrev {
		t.hasPrev = true
		t.prev = c
		return c
	}

	d := Cumulative{
		Input:           deltaField(t.prev.Input, c.Input),
		CachedInput:     deltaField(t.prev.CachedInput, c.CachedInput),
		Output:          deltaField(t.prev.Output, c.Output),
		ReasoningOutput: deltaField(t.prev.ReasoningOutput, c.ReasoningOutput),
		Total:           deltaField(t.prev.Total, c.Total),
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
