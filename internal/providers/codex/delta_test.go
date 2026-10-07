// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package codex

import "testing"

func TestTrackerDelta(t *testing.T) {
	tests := []struct {
		name   string
		inputs []int64 // successive cumulative Input observations
		want   []int64 // expected deltas, one per observation
	}{
		{
			name:   "monotonic growth",
			inputs: []int64{100, 250, 400},
			want:   []int64{100, 150, 150},
		},
		{
			name:   "reset mid-sequence never goes negative",
			inputs: []int64{100, 250, 80},
			want:   []int64{100, 150, 80},
		},
		{
			name:   "first observation on a new tracker is the full value",
			inputs: []int64{42},
			want:   []int64{42},
		},
		{
			name:   "repeated identical observation yields zero delta",
			inputs: []int64{100, 100},
			want:   []int64{100, 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := NewTracker()
			for i, in := range tt.inputs {
				got := tr.Delta(Cumulative{Input: in}).Input
				if got != tt.want[i] {
					t.Fatalf("observation %d: Delta(Input=%d).Input = %d, want %d", i, in, got, tt.want[i])
				}
			}
		})
	}
}

func TestTrackerDeltaNeverNegative(t *testing.T) {
	sequences := [][]Cumulative{
		{
			{Input: 100, Output: 30, CachedInput: 0, ReasoningOutput: 0, Total: 130},
			{Input: 250, Output: 60, CachedInput: 20, ReasoningOutput: 5, Total: 330},
			{Input: 80, Output: 25, CachedInput: 5, ReasoningOutput: 2, Total: 110},
			{Input: 90, Output: 10, CachedInput: 0, ReasoningOutput: 0, Total: 100},
		},
	}

	for _, seq := range sequences {
		tr := NewTracker()
		for i, c := range seq {
			d := tr.Delta(c)
			fields := map[string]int64{
				"Input":           d.Input,
				"Output":          d.Output,
				"CachedInput":     d.CachedInput,
				"ReasoningOutput": d.ReasoningOutput,
				"Total":           d.Total,
			}
			for name, v := range fields {
				if v < 0 {
					t.Fatalf("observation %d: delta field %s = %d, want >= 0", i, name, v)
				}
			}
		}
	}
}

func TestTrackerPerFieldIndependence(t *testing.T) {
	tr := NewTracker()
	tr.Delta(Cumulative{Input: 100, Output: 50, CachedInput: 10, ReasoningOutput: 5, Total: 160})

	// Output resets while Input and CachedInput keep growing: each field's
	// reset detection must be independent of the others.
	got := tr.Delta(Cumulative{Input: 150, Output: 20, CachedInput: 15, ReasoningOutput: 1, Total: 185})
	want := Cumulative{Input: 50, Output: 20, CachedInput: 5, ReasoningOutput: 1, Total: 25}
	if got != want {
		t.Fatalf("Delta() = %+v, want %+v", got, want)
	}
}

func TestTrackerDuplicateObservationYieldsZeroDelta(t *testing.T) {
	// Codex emits a duplicate token_count for the same turn in some
	// sessions (observed in real rollouts). Feeding total_token_usage
	// through Tracker makes the duplicate a zero delta instead of a
	// double-count, which is the whole reason codex.go feeds Tracker from
	// total_token_usage and never from last_token_usage.
	tr := NewTracker()
	first := tr.Delta(Cumulative{Input: 15448, Output: 42, CachedInput: 12000, ReasoningOutput: 10, Total: 15490})
	if first.Total != 15490 {
		t.Fatalf("first observation Total = %d, want 15490", first.Total)
	}

	dup := tr.Delta(Cumulative{Input: 15448, Output: 42, CachedInput: 12000, ReasoningOutput: 10, Total: 15490})
	want := Cumulative{}
	if dup != want {
		t.Fatalf("duplicate observation Delta() = %+v, want all-zero %+v", dup, want)
	}
}
