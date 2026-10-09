// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package aggregate

import (
	"reflect"
	"testing"
	"time"
)

func TestWindows(t *testing.T) {
	tz, err := time.LoadLocation("America/Mexico_City")
	if err != nil {
		t.Fatalf("load tz: %v", err)
	}
	now := time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		eventAt time.Time
		want    []Window
	}{
		{
			name:    "2 hours ago falls in every trailing window plus mtd and all",
			eventAt: now.Add(-2 * time.Hour),
			want:    []Window{Window24h, Window7d, Window30d, WindowMTD, WindowAll},
		},
		{
			name:    "exactly one hour ago belongs to the 1h window",
			eventAt: now.Add(-time.Hour),
			want:    []Window{Window1h, Window24h, Window7d, Window30d, WindowMTD, WindowAll},
		},
		{
			name:    "10 days ago is outside 24h/7d but still this month",
			eventAt: now.Add(-10 * 24 * time.Hour),
			want:    []Window{Window30d, WindowMTD, WindowAll},
		},
		{
			name:    "over a year ago, different month and year, only all",
			eventAt: time.Date(2025, 2, 28, 10, 0, 0, 0, time.UTC),
			want:    []Window{WindowAll},
		},
		{
			name:    "same day, but in the future relative to now",
			eventAt: now.Add(1 * time.Hour),
			want:    []Window{WindowAll},
		},
		{
			name:    "exactly now",
			eventAt: now,
			want:    []Window{Window1h, Window24h, Window7d, Window30d, WindowMTD, WindowAll},
		},
		{
			name:    "earlier same local month, before day 1 boundary in UTC but after it in tz",
			eventAt: time.Date(2026, 3, 1, 4, 0, 0, 0, time.UTC), // 2026-02-28T22:00 in America/Mexico_City
			want:    []Window{Window30d, WindowAll},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Windows(tt.eventAt, now, tz)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Windows() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWindowsNilLocationDefaultsToUTC(t *testing.T) {
	now := time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC)
	got := Windows(now.Add(-time.Hour), now, nil)
	want := []Window{Window1h, Window24h, Window7d, Window30d, WindowMTD, WindowAll}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Windows() with nil tz = %v, want %v", got, want)
	}
}
