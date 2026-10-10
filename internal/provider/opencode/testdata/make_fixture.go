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
// raw JSON stored in message.data, including its "role" field - the real
// `message` table has no role column (see Build's schema), so every
// fixture row's role, like a real row's, lives only inside Data.
type Message struct {
	ID        string
	SessionID string
	Data      string
}

// Part describes one fixture row for OpenCode's `part` table. Type is held
// inside Data, as it is in the real schema. Its payload is intentionally not
// modeled because the provider only needs that discriminator to count calls.
type Part struct {
	ID        string
	MessageID string
	SessionID string
	Data      string
}

// DefaultSessions and DefaultMessages are redacted extracts of real
// opencode.db rows (JCB-322): path.cwd, path.root and summary content are
// replaced with "REDACTED", but role, modelID, providerID, time.created,
// cost and the full tokens block are kept verbatim so the fixture exercises
// the same arithmetic a real database would. A fixture built from a made-up
// schema is exactly how JCB-322's bug (filtering on a m.role column that
// does not exist) and ADR-004's bug (assuming reasoning is always additive)
// both slipped past green tests in PR #8.
//
// Required cases, each traceable to a real row:
//   - msg_1: additive reasoning>0 (opencode/nemotron-3-super-free).
//   - msg_2: nested reasoning>0, the opencode-go/kimi-k2.5 pattern that
//     ADR-004 documents as the one non-additive pair on real data. Its
//     path.cwd carries the sentinel string, not "REDACTED", so this message
//     doubles as the proof that Parse never decodes path.cwd into an event.
//   - msg_3: no tokens.total at all (673/18423 real assistant rows).
//   - msg_4: tokens.cache missing entirely (schema tolerance).
//   - msg_5: invalid JSON, must be skipped without aborting the scan.
//   - msg_6: role "user", must be excluded by the WHERE clause before
//     m.data is ever decoded.
//   - msg_7: nested reasoning>output (opencode-go/kimi-k2.5, redacted from
//     msg_d6498c343002a4B11hEll2WOjs, JCB-323). Self-contradictory on
//     origin: total == i+o+cr+cw says reasoning travels inside output, but
//     reasoning(88) > output(85) says it does not fit. ADR-004 keeps the
//     nested max(0, output-reasoning) branch anyway (lowest error bound of
//     the two), so this record's five emitted classes sum to
//     total+(reasoning-output), not total - the one documented exception
//     to the per-record five-class invariant.
var DefaultSessions = []Session{
	{ID: "ses_alpha", Directory: "/home/user/projects/alpha"},
	{ID: "ses_beta", Directory: "/home/user/projects/beta"},
}

var DefaultMessages = []Message{
	{
		ID: "msg_1", SessionID: "ses_alpha",
		Data: `{"parentID":"msg_parent1","role":"assistant","mode":"plan","agent":"plan",` +
			`"path":{"cwd":"REDACTED","root":"REDACTED"},"cost":0,` +
			`"tokens":{"total":96583,"input":95189,"output":934,"reasoning":460,"cache":{"write":0,"read":0}},` +
			`"modelID":"nemotron-3-super-free","providerID":"opencode",` +
			`"time":{"created":1777486305093,"completed":1777486398749},"finish":"stop"}`,
	},
	{
		ID: "msg_2", SessionID: "ses_alpha",
		Data: `{"role":"assistant","time":{"created":1772667137548,"completed":1772667141332},` +
			`"parentID":"msg_parent2","modelID":"kimi-k2.5","providerID":"opencode-go","mode":"build","agent":"build",` +
			`"path":{"cwd":"SENTINEL-PROMPT-TEXT","root":"REDACTED"},"cost":0.0058876,` +
			`"tokens":{"total":9784,"input":9495,"output":33,"reasoning":22,"cache":{"read":256,"write":0}},"finish":"stop"}`,
	},
	{
		ID: "msg_3", SessionID: "ses_beta",
		Data: `{"role":"assistant","time":{"created":1769550508420,"completed":1769550508742},` +
			`"parentID":"msg_parent3","modelID":"moonshotai/kimi-k2:free","providerID":"openrouter",` +
			`"mode":"build","agent":"build","path":{"cwd":"REDACTED","root":"REDACTED"},"cost":0,` +
			`"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}}}`,
	},
	{
		ID: "msg_4", SessionID: "ses_beta",
		Data: `{"role":"assistant","time":{"created":1733097660000},"modelID":"claude-sonnet-4-5",` +
			`"providerID":"anthropic","path":{"cwd":"REDACTED","root":"REDACTED"},"cost":0.0245,` +
			`"tokens":{"input":200,"output":75,"reasoning":0}}`,
	},
	{
		ID: "msg_5", SessionID: "ses_beta",
		Data: `not valid json {{{`,
	},
	{
		ID: "msg_6", SessionID: "ses_alpha",
		Data: `{"role":"user","time":{"created":1769550508409},"summary":{"diffs":[]},"agent":"build",` +
			`"model":{"providerID":"openrouter","modelID":"moonshotai/kimi-k2:free"}}`,
	},
	{
		ID: "msg_7", SessionID: "ses_alpha",
		Data: `{"role":"assistant","time":{"created":1778360729384,"completed":1778360774215},` +
			`"parentID":"msg_parent7","modelID":"kimi-k2.5","providerID":"opencode-go","mode":"build","agent":"build",` +
			`"path":{"cwd":"REDACTED","root":"REDACTED"},"cost":0.0066421,` +
			`"tokens":{"total":94158,"input":7289,"output":85,"reasoning":88,"cache":{"read":86784,"write":0}},"finish":"stop"}`,
	},
}

// DefaultParts is a redacted extract of real part rows. It covers tool,
// text, and reasoning parts plus a tool part whose message no longer exists;
// only tool parts attached to an emitted assistant message count.
var DefaultParts = []Part{
	{ID: "part_1", MessageID: "msg_1", SessionID: "ses_alpha", Data: `{"type":"tool","callID":"REDACTED","tool":"REDACTED","state":{"status":"completed"}}`},
	{ID: "part_2", MessageID: "msg_1", SessionID: "ses_alpha", Data: `{"type":"tool","callID":"REDACTED","tool":"REDACTED","state":{"status":"completed"}}`},
	{ID: "part_3", MessageID: "msg_1", SessionID: "ses_alpha", Data: `{"type":"text","text":"REDACTED"}`},
	{ID: "part_4", MessageID: "msg_2", SessionID: "ses_alpha", Data: `{"type":"reasoning","text":"REDACTED"}`},
	{ID: "part_5", MessageID: "msg_2", SessionID: "ses_alpha", Data: `{"type":"tool","callID":"REDACTED","tool":"REDACTED","state":{"status":"completed"}}`},
	{ID: "part_orphan", MessageID: "missing_message", SessionID: "ses_beta", Data: `{"type":"tool","callID":"REDACTED","tool":"REDACTED","state":{"status":"completed"}}`},
}

// Build creates opencode.db under dir with the `session` and `message`
// tables populated from sessions and messages, and returns its full path.
//
// The `message` CREATE TABLE below is copied verbatim from the `.schema
// message` output of a real opencode.db, FK and index included: it has no
// `role` column, because role lives inside the data JSON like every other
// field this provider reads. JCB-322's bug - WHERE m.role = 'assistant',
// a query that errors with "no such column: m.role" on every real
// database - passed PR #8's tests only because that fixture invented a
// role column that does not exist. If this schema ever grows a role
// column again, it has drifted from reality the same way.
func Build(dir string, sessions []Session, messages []Message) (string, error) {
	return BuildWithParts(dir, sessions, messages, DefaultParts)
}

// BuildWithParts creates the fixture with explicitly supplied part rows.
func BuildWithParts(dir string, sessions []Session, messages []Message, parts []Part) (string, error) {
	path := filepath.Join(dir, "opencode.db")

	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return "", fmt.Errorf("testdata: open %s: %w", path, err)
	}
	defer db.Close()

	const schema = "" +
		"CREATE TABLE session (\n" +
		"\tid        TEXT PRIMARY KEY,\n" +
		"\tdirectory TEXT NOT NULL\n" +
		");\n" +
		"CREATE TABLE `message` (\n" +
		"\t`id` text PRIMARY KEY,\n" +
		"\t`session_id` text NOT NULL,\n" +
		"\t`time_created` integer NOT NULL,\n" +
		"\t`time_updated` integer NOT NULL,\n" +
		"\t`data` text NOT NULL,\n" +
		"\tCONSTRAINT `fk_message_session_id_session_id_fk` FOREIGN KEY (`session_id`) REFERENCES `session`(`id`) ON DELETE CASCADE\n" +
		");\n" +
		"CREATE INDEX `message_session_time_created_id_idx` ON `message` (`session_id`,`time_created`,`id`);" +
		"CREATE TABLE `part` (\n" +
		"\t`id` text PRIMARY KEY,\n" +
		"\t`message_id` text NOT NULL,\n" +
		"\t`session_id` text NOT NULL,\n" +
		"\t`time_created` integer NOT NULL,\n" +
		"\t`time_updated` integer NOT NULL,\n" +
		"\t`data` text NOT NULL\n" +
		");\n" +
		"CREATE INDEX `part_message_id_idx` ON `part` (`message_id`);"
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
			`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, 0, 0, ?)`,
			m.ID, m.SessionID, m.Data,
		); err != nil {
			return "", fmt.Errorf("testdata: insert message %s: %w", m.ID, err)
		}
	}
	for _, p := range parts {
		if _, err := db.Exec(
			`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, 0, 0, ?)`,
			p.ID, p.MessageID, p.SessionID, p.Data,
		); err != nil {
			return "", fmt.Errorf("testdata: insert part %s: %w", p.ID, err)
		}
	}

	return path, nil
}
