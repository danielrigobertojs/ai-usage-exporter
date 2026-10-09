// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package aggregate

import "time"

// Window is one of the aggregation windows gauges are reported for. See
// docs/metrics.md for the window label values this maps to.
type Window string

const (
	Window1h  Window = "1h"
	Window24h Window = "24h"
	Window7d  Window = "7d"
	Window30d Window = "30d"
	WindowMTD Window = "mtd"
	WindowAll Window = "all"
)

// orderedWindows fixes the iteration order Windows reports membership in,
// so callers (and tests) get a deterministic slice.
var orderedWindows = []Window{Window1h, Window24h, Window7d, Window30d, WindowMTD, WindowAll}

// AllWindows returns every supported aggregation window in reporting order.
// The returned slice is independent, so callers cannot change this package's
// window contract.
func AllWindows() []Window {
	return append([]Window(nil), orderedWindows...)
}

// Windows returns the windows an event at eventAt belongs to, given a scan
// instant now and the reporting timezone tz. The 1h/24h/7d/30d windows are
// plain trailing durations; mtd is a Gregorian calendar-month boundary
// anchored at local midnight on the 1st of the month in tz, computed from
// calendar year/month equality rather than a fixed duration, so DST
// transitions and leap years never shift it. all always matches.
func Windows(eventAt, now time.Time, tz *time.Location) []Window {
	if tz == nil {
		tz = time.UTC
	}

	elapsed := now.Sub(eventAt)
	inFuture := eventAt.After(now)

	result := make([]Window, 0, len(orderedWindows))
	if !inFuture && elapsed <= time.Hour {
		result = append(result, Window1h)
	}
	if !inFuture && elapsed <= 24*time.Hour {
		result = append(result, Window24h)
	}
	if !inFuture && elapsed <= 7*24*time.Hour {
		result = append(result, Window7d)
	}
	if !inFuture && elapsed <= 30*24*time.Hour {
		result = append(result, Window30d)
	}
	if !inFuture && sameCalendarMonth(eventAt, now, tz) {
		result = append(result, WindowMTD)
	}
	result = append(result, WindowAll)
	return result
}

func sameCalendarMonth(eventAt, now time.Time, tz *time.Location) bool {
	eventYear, eventMonth, _ := eventAt.In(tz).Date()
	nowYear, nowMonth, _ := now.In(tz).Date()
	return eventYear == nowYear && eventMonth == nowMonth
}
