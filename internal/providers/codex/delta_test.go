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
			{Input: 100, Output: 30, Cached: 0, Total: 130},
			{Input: 250, Output: 60, Cached: 20, Total: 330},
			{Input: 80, Output: 25, Cached: 5, Total: 110},
			{Input: 90, Output: 10, Cached: 0, Total: 100},
		},
	}

	for _, seq := range sequences {
		tr := NewTracker()
		for i, c := range seq {
			d := tr.Delta(c)
			for name, v := range map[string]int64{"Input": d.Input, "Output": d.Output, "Cached": d.Cached, "Total": d.Total} {
				if v < 0 {
					t.Fatalf("observation %d: delta field %s = %d, want >= 0", i, name, v)
				}
			}
		}
	}
}

func TestTrackerPerFieldIndependence(t *testing.T) {
	tr := NewTracker()
	tr.Delta(Cumulative{Input: 100, Output: 50, Cached: 10, Total: 160})

	// Output resets while Input and Cached keep growing: each field's reset
	// detection must be independent of the others.
	got := tr.Delta(Cumulative{Input: 150, Output: 20, Cached: 15, Total: 185})
	want := Cumulative{Input: 50, Output: 20, Cached: 5, Total: 25}
	if got != want {
		t.Fatalf("Delta() = %+v, want %+v", got, want)
	}
}
