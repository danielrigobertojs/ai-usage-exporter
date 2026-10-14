// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/aggregate"

	_ "modernc.org/sqlite"
)

func TestRunWritesOpenCodeAtProviderPath(t *testing.T) {
	out := t.TempDir()
	if err := run(out, time.Now().UTC().Truncate(time.Minute)); err != nil {
		t.Fatalf("run(%q): %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(out, "opencode", "opencode.db")); err != nil {
		t.Fatalf("OpenCode database not at provider path: %v", err)
	}
}

func TestParseBaseDefaultsToCurrentMinute(t *testing.T) {
	now := time.Date(2026, 10, 13, 8, 31, 47, 0, time.FixedZone("test", -6*60*60))
	got, err := parseBase("", now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 13, 14, 31, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("default base = %s, want %s", got, want)
	}
}

func TestGeneratedEventsAreVisibleInRecentWindows(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	out := t.TempDir()
	if err := run(out, now); err != nil {
		t.Fatal(err)
	}

	assertRecentGeneratedEvents(t, out, now)
}

func TestExecuteDefaultGeneratesRecentEvents(t *testing.T) {
	now := time.Date(2026, 10, 13, 18, 41, 20, 0, time.UTC)
	out := t.TempDir()
	if err := execute([]string{"-out", out}, now); err != nil {
		t.Fatal(err)
	}
	assertRecentGeneratedEvents(t, out, now.Truncate(time.Minute))
}

func assertRecentGeneratedEvents(t *testing.T, out string, now time.Time) {
	t.Helper()
	timestamps := generatedTimestamps(t, out)
	if len(timestamps) != 18 {
		t.Fatalf("timestamps = %d, want 18", len(timestamps))
	}
	for _, timestamp := range timestamps {
		windows := aggregate.Windows(timestamp, now, time.UTC)
		if !slices.Contains(windows, aggregate.Window1h) || !slices.Contains(windows, aggregate.Window24h) {
			t.Fatalf("event at %s missing recent windows: %v", timestamp, windows)
		}
	}
}

func generatedTimestamps(t *testing.T, out string) []time.Time {
	t.Helper()
	var timestamps []time.Time
	for _, path := range []string{
		filepath.Join(out, ".claude", "projects", "project-alpha", "demo-claude.jsonl"),
		filepath.Join(out, ".codex", "sessions", "2026", "10", "11", "rollout-demo.jsonl"),
	} {
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		s := bufio.NewScanner(f)
		for s.Scan() {
			var line struct {
				Type      string `json:"type"`
				Timestamp string `json:"timestamp"`
			}
			if err := json.Unmarshal(s.Bytes(), &line); err != nil {
				t.Fatal(err)
			}
			if line.Type != "assistant" && line.Type != "event_msg" {
				continue
			}
			ts, err := time.Parse(time.RFC3339, line.Timestamp)
			if err != nil {
				t.Fatal(err)
			}
			timestamps = append(timestamps, ts)
		}
		if err := s.Err(); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	db, err := sql.Open("sqlite", filepath.Join(out, "opencode", "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT json_extract(data, '$.time.created') FROM message`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var millis int64
		if err := rows.Scan(&millis); err != nil {
			t.Fatal(err)
		}
		timestamps = append(timestamps, time.UnixMilli(millis).UTC())
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return timestamps
}
