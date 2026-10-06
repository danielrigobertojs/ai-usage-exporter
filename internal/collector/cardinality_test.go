// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package collector

import (
	"strings"
	"testing"

	dto "github.com/prometheus/client_model/go"
)

func TestNormalizeProjectLabelDisabledNeverEmits(t *testing.T) {
	if _, ok := NormalizeProjectLabel("myproj", false, 0); ok {
		t.Fatal("NormalizeProjectLabel: ok = true with enabled=false, want false")
	}
}

func TestNormalizeProjectLabelEmptyRawNeverEmits(t *testing.T) {
	if _, ok := NormalizeProjectLabel("", true, 0); ok {
		t.Fatal(`NormalizeProjectLabel: ok = true with raw="", want false`)
	}
}

// TestNormalizeProjectLabelTruncatesToMax covers step 7: a 200-character
// ProjectID is truncated to 48 (DefaultMaxProjectLabel) when enabled and no
// explicit max is given.
func TestNormalizeProjectLabelTruncatesToMax(t *testing.T) {
	raw := strings.Repeat("a", 200)
	value, ok := NormalizeProjectLabel(raw, true, 0)
	if !ok {
		t.Fatal("NormalizeProjectLabel: ok = false, want true")
	}
	if len(value) != DefaultMaxProjectLabel {
		t.Errorf("len(value) = %d, want %d", len(value), DefaultMaxProjectLabel)
	}
}

func TestNormalizeProjectLabelHonorsExplicitMax(t *testing.T) {
	raw := strings.Repeat("b", 200)
	value, ok := NormalizeProjectLabel(raw, true, 10)
	if !ok {
		t.Fatal("NormalizeProjectLabel: ok = false, want true")
	}
	if len(value) != 10 {
		t.Errorf("len(value) = %d, want 10", len(value))
	}
}

func TestNormalizeProjectLabelUnderLimitUntouched(t *testing.T) {
	value, ok := NormalizeProjectLabel("short", true, 0)
	if !ok || value != "short" {
		t.Errorf("NormalizeProjectLabel(%q) = %q, %v; want %q, true", "short", value, ok, "short")
	}
}

// TestNoSeriesEverCarriesASessionLabel covers step 7: under no combination
// of Options does any emitted series carry a label named "session" or
// "session_id" - the cardinality trap docs/metrics.md calls out.
func TestNoSeriesEverCarriesASessionLabel(t *testing.T) {
	for _, opts := range []Options{
		{},
		{ProjectLabel: false, MaxProjectLabel: 48},
		{ProjectLabel: true},
		{ProjectLabel: true, MaxProjectLabel: 10},
	} {
		c := New(fixedCatalog(), opts)
		c.Set(fixedResult())

		for _, mf := range mustGather(t, c) {
			for _, m := range mf.Metric {
				for _, lp := range m.Label {
					if forbiddenLabelNames[lp.GetName()] {
						t.Errorf("opts=%+v: metric %s carries forbidden label %q", opts, mf.GetName(), lp.GetName())
					}
				}
			}
		}
	}
}

// TestNoSeriesEverCarriesAProjectLabel documents the current state
// honestly: aggregate.Snapshot has no per-project dimension yet (see the
// Options.ProjectLabel doc comment in collector.go), so even with
// ProjectLabel enabled no emitted series carries a project label today.
// This test exists to catch the day that stops being true by accident,
// before a real per-project series is wired in deliberately.
func TestNoSeriesEverCarriesAProjectLabel(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		c := New(fixedCatalog(), Options{ProjectLabel: enabled})
		c.Set(fixedResult())

		if hasAnyLabelNamed(mustGather(t, c), "project") {
			t.Errorf("ProjectLabel=%v: a series carries a %q label, but aggregate.Snapshot has no project dimension to source it from", enabled, "project")
		}
	}
}

func hasAnyLabelNamed(families []*dto.MetricFamily, name string) bool {
	for _, mf := range families {
		for _, m := range mf.Metric {
			for _, lp := range m.Label {
				if lp.GetName() == name {
					return true
				}
			}
		}
	}
	return false
}
