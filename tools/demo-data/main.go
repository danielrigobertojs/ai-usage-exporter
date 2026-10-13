// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval
// Command demo-data creates deterministic, synthetic provider logs for demos.
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	out := flag.String("out", "./demo-data", "directory for synthetic provider data")
	baseFlag := flag.String("base", "", "RFC3339 timestamp anchoring generated events (default: current minute)")
	flag.Parse()
	base, err := parseBase(*baseFlag, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := run(*out, base); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// parseBase returns a minute-stable current timestamp by default, or the
// explicit RFC3339 anchor requested for reproducible fixtures.
func parseBase(raw string, now time.Time) (time.Time, error) {
	if raw == "" {
		return now.UTC().Truncate(time.Minute), nil
	}
	base, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse -base: %w", err)
	}
	return base.UTC(), nil
}

func run(out string, base time.Time) error {
	if err := os.MkdirAll(filepath.Join(out, ".claude", "projects", "project-alpha"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(out, ".codex", "sessions", "2026", "10", "11"), 0o755); err != nil {
		return err
	}
	claude := filepath.Join(out, ".claude", "projects", "project-alpha", "demo-claude.jsonl")
	cf, err := os.Create(claude)
	if err != nil {
		return err
	}
	defer cf.Close()
	for i := 0; i < 6; i++ {
		line := map[string]any{"type": "assistant", "uuid": fmt.Sprintf("claude-%02d", i), "sessionId": "demo-claude", "cwd": "project-alpha", "timestamp": base.Add(time.Duration(-50+i*10) * time.Minute).Format(time.RFC3339), "message": map[string]any{"model": []string{"claude-sonnet-4-5", "claude-opus-4-1"}[i%2], "usage": map[string]int{"input_tokens": 1200 + i*83, "output_tokens": 420 + i*31, "cache_read_input_tokens": 800 + i*67, "cache_creation_input_tokens": 120 + i*11}}}
		b, _ := json.Marshal(line)
		fmt.Fprintln(cf, string(b))
	}
	codex := filepath.Join(out, ".codex", "sessions", "2026", "10", "11", "rollout-demo.jsonl")
	xf, err := os.Create(codex)
	if err != nil {
		return err
	}
	defer xf.Close()
	write := func(v any) { b, _ := json.Marshal(v); fmt.Fprintln(xf, string(b)) }
	write(map[string]any{"type": "session_meta", "timestamp": base.Add(-55 * time.Minute).Format(time.RFC3339), "payload": map[string]any{"id": "demo-codex", "model": "gpt-5"}})
	for i := 1; i <= 6; i++ {
		write(map[string]any{"type": "event_msg", "timestamp": base.Add(time.Duration(-55+i*9) * time.Minute).Format(time.RFC3339), "payload": map[string]any{"type": "token_count", "info": map[string]any{"total_token_usage": map[string]int{"input_tokens": i * 1800, "cached_input_tokens": i * 900, "output_tokens": i * 500, "reasoning_output_tokens": i * 130, "total_tokens": i * 2300}}}})
	}
	if err := os.MkdirAll(filepath.Join(out, "opencode"), 0o755); err != nil {
		return err
	}
	dbPath := filepath.Join(out, "opencode", "opencode.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT); CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, data TEXT); CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, data TEXT); INSERT INTO session VALUES ('demo-open','project-alpha');`); err != nil {
		return err
	}
	for i := 0; i < 6; i++ {
		created := base.Add(time.Duration(-48+i*8) * time.Minute).UnixMilli()
		data := fmt.Sprintf(`{"role":"assistant","modelID":"gpt-4.1","providerID":"openai","time":{"created":%d},"tokens":{"total":2100,"input":900,"output":500,"cache":{"read":600,"write":50},"reasoning":50},"cost":0.02}`, created)
		if _, err = db.Exec("INSERT INTO message VALUES (?,?,?)", fmt.Sprintf("open-%02d", i), "demo-open", data); err != nil {
			return err
		}
		if _, err = db.Exec("INSERT INTO part VALUES (?,?,?)", fmt.Sprintf("part-%02d", i), fmt.Sprintf("open-%02d", i), `{"type":"tool"}`); err != nil {
			return err
		}
	}
	return nil
}
