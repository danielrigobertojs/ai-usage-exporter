// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

// Package testdata builds disposable opencode.db SQLite fixtures for
// opencode_test.go. It lives under testdata/ (so "go build ./..." never
// picks it up as a shipped package) but is imported explicitly by the
// opencode package's tests - the go tool only skips testdata directories
// when expanding "...", not when a path inside one is imported by name.
//
// No binary .db is committed: every test calls Build against its own
// t.TempDir(), per the project's testdata-is-generated-not-committed rule.
package testdata

import (
	"database/sql"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Session describes one fixture row for the `session` table.
type Session struct {
	ID        string
	Directory string
}

// Message describes one fixture row for the `message` table. Data is the
// raw JSON stored in message.data; Role is usually "assistant" or "user".
type Message struct {
	ID        string
	SessionID string
	Role      string
	Data      string
}

// DefaultSessions and DefaultMessages are the two sessions and six messages
// (five assistant, one user) JCB-312 step 1 asks for: realistic token and
// cost JSON, one message missing tokens.cache entirely (schema-tolerance),
// one with invalid JSON (skip-don't-abort), and a sentinel prompt string
// that must never reach an emitted event.
var DefaultSessions = []Session{
	{ID: "ses_alpha", Directory: "/home/user/projects/alpha"},
	{ID: "ses_beta", Directory: "/home/user/projects/beta"},
}

var DefaultMessages = []Message{
	{
		ID: "msg_1", SessionID: "ses_alpha", Role: "assistant",
		Data: `{"modelID":"claude-sonnet-4-5","providerID":"anthropic","time":{"created":1733097600000},` +
			`"tokens":{"input":100,"output":50,"cache":{"read":10,"write":5},"reasoning":0},` +
			`"cost":0.0123,"text":"SENTINEL-PROMPT-TEXT"}`,
	},
	{
		ID: "msg_2", SessionID: "ses_alpha", Role: "assistant",
		Data: `{"modelID":"claude-sonnet-4-5","providerID":"anthropic","time":{"created":1733097660000},` +
			`"tokens":{"input":200,"output":75,"reasoning":3},` +
			`"cost":0.0245,"text":"SENTINEL-PROMPT-TEXT"}`,
	},
	{
		ID: "msg_3", SessionID: "ses_beta", Role: "assistant",
		Data: `{"modelID":"gpt-5-codex","providerID":"openai","time":{"created":1733101200000},` +
			`"tokens":{"input":300,"output":120,"cache":{"read":40,"write":0},"reasoning":10},` +
			`"cost":0.0510,"text":"SENTINEL-PROMPT-TEXT"}`,
	},
	{
		ID: "msg_4", SessionID: "ses_beta", Role: "assistant",
		Data: `not valid json {{{`,
	},
	{
		ID: "msg_5", SessionID: "ses_beta", Role: "assistant",
		Data: `{"modelID":"gpt-5-codex","providerID":"openai","time":{"created":1733101260000},` +
			`"tokens":{"input":15,"output":5},"cost":0.001,"text":"SENTINEL-PROMPT-TEXT"}`,
	},
	{
		ID: "msg_6", SessionID: "ses_alpha", Role: "user",
		Data: `{"text":"SENTINEL-PROMPT-TEXT"}`,
	},
}

// Build creates opencode.db under dir with the `session` and `message`
// tables populated from sessions and messages, and returns its full path.
func Build(dir string, sessions []Session, messages []Message) (string, error) {
	path := filepath.Join(dir, "opencode.db")

	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return "", fmt.Errorf("testdata: open %s: %w", path, err)
	}
	defer db.Close()

	const schema = `
CREATE TABLE session (
	id        TEXT PRIMARY KEY,
	directory TEXT NOT NULL
);
CREATE TABLE message (
	id         TEXT PRIMARY KEY,
	session_id TEXT NOT NULL,
	role       TEXT NOT NULL,
	data       TEXT NOT NULL
);`
	if _, err := db.Exec(schema); err != nil {
		return "", fmt.Errorf("testdata: create schema: %w", err)
	}

	for _, s := range sessions {
		if _, err := db.Exec(`INSERT INTO session (id, directory) VALUES (?, ?)`, s.ID, s.Directory); err != nil {
			return "", fmt.Errorf("testdata: insert session %s: %w", s.ID, err)
		}
	}
	for _, m := range messages {
		if _, err := db.Exec(
			`INSERT INTO message (id, session_id, role, data) VALUES (?, ?, ?, ?)`,
			m.ID, m.SessionID, m.Role, m.Data,
		); err != nil {
			return "", fmt.Errorf("testdata: insert message %s: %w", m.ID, err)
		}
	}

	return path, nil
}
