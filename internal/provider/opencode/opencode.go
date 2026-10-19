// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

// Package opencode implements the provider.Provider for OpenCode, the only
// supported tool that keeps its history in SQLite instead of JSONL:
// ~/.local/share/opencode/opencode.db. OpenCode has a legacy schema and a
// newer family of schemas whose table names can change between generations,
// with
// tokens and native cost living inside message.data's JSON blob and tool-call
// counts in part.data.
//
// The operational risk here isn't parsing, it's the database connection
// itself: opencode.db can be open and in WAL mode under a live agent, and
// opening it for write - or with immutable=1, which is simply wrong for a
// database that is actually changing - risks corrupting it or leaving a
// stray -wal file behind. DSN's exact query parameters (mode=ro,
// query_only(1), busy_timeout(2000)) are the contract that keeps every
// connection this package opens strictly read-only, non-blocking-forever,
// and honest about being read-only.
package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/privacy"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider"
)

const toolID = "opencode"

// New returns the OpenCode provider.Provider.
func New() provider.Provider {
	return openCodeProvider{}
}

type openCodeProvider struct{}

func (openCodeProvider) Descriptor() provider.Descriptor {
	return provider.Descriptor{
		ID:          toolID,
		DisplayName: "OpenCode",
		Kind:        provider.SourceSQLite,
		HomeEnv:     []string{"OPENCODE_DATA_HOME"},
		Roots: []provider.RootSpec{
			{Base: provider.BaseXDGData, Rel: "opencode", Glob: "opencode.db"},
			{GOOS: "windows", Base: provider.BaseLocalAppData, Rel: "opencode", Glob: "opencode.db"},
		},
		Capabilities: provider.Capabilities{
			HasTokens:      true,
			HasCacheTokens: true,
			HasToolCalls:   true,
			HasNativeCost:  true,
		},
	}
}

// DSN builds the read-only connection string for the opencode.db at path.
// Its exact shape is an acceptance criterion, not an implementation detail:
// mode=ro refuses to create or write the file, query_only(1) rejects any
// write even if mode=ro were ever bypassed, and busy_timeout(2000) bounds
// how long a query waits on a lock held by the live agent instead of
// hanging forever.
func DSN(path string) string {
	return "file:" + path + "?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(2000)"
}

// message.role is not a column: OpenCode's `message` table is just
// (id, session_id, time_created, time_updated, data), and role lives inside
// the data JSON blob like everything else. Filtering on m.role instead of
// json_extract(m.data, '$.role') is a SQLite "no such column" error on every
// real database - see docs/providers/opencode.md.
//
// json_valid(m.data) must come first: SQLite short-circuits AND, and
// json_extract raises "malformed JSON" instead of returning NULL when data
// isn't valid JSON at all. Without the guard, one corrupt row would abort
// the query for every row instead of just being excluded by this filter -
// it still gets a second, Go-level skip in the row loop below, since a row
// can be valid JSON and still not decode into messageData the way Parse
// expects.
const legacyQuery = `
SELECT m.id, m.session_id, m.data, s.directory,
       (
               SELECT COUNT(*)
               FROM part p
               WHERE p.message_id = m.id
                 AND json_valid(p.data)
                 AND json_extract(p.data, '$.type') = 'tool'
       ) AS tool_calls,
       m.id AS legacy_id,
       json_extract(m.data, '$.tokens.total') AS legacy_total
FROM message m JOIN session s ON s.id = m.session_id
WHERE json_valid(m.data) AND json_extract(m.data, '$.role') = 'assistant'
ORDER BY m.id`

// currentQuery deliberately counts only the type discriminator inside
// content. content itself can contain model reasoning and other text, so it
// is never selected, unmarshaled, or copied into a UsageEvent. Table names
// are quoted separately after being obtained from SQLite's own schema.
const currentQuery = `
SELECT m.id, m.session_id, m.data, s.directory,
       (
               SELECT COUNT(*)
               FROM json_each(m.data, '$.content') content
               WHERE json_extract(content.value, '$.type') = 'tool'
       ) AS tool_calls,
       NULL AS legacy_id,
       NULL AS legacy_total
FROM %s m JOIN %s s ON s.id = m.session_id
WHERE m.type = 'assistant' AND json_valid(m.data)
ORDER BY m.id`

// currentWithLegacyMetadataQuery is deliberately a narrow, indexed lookup
// into the obsolete generation. The current generation remains the sole
// source of events; the legacy row contributes only its matching ID and
// tokens.total as evidence for the representation of reasoning. In
// particular, this query never projects legacy content, role, path, or model.
const currentWithLegacyMetadataQuery = `
SELECT m.id, m.session_id, m.data, s.directory,
       (
               SELECT COUNT(*)
               FROM json_each(m.data, '$.content') content
               WHERE json_extract(content.value, '$.type') = 'tool'
       ) AS tool_calls,
       legacy.id AS legacy_id,
       CASE WHEN json_valid(legacy.data)
                 AND json_type(legacy.data, '$.tokens.total') = 'integer'
            THEN json_extract(legacy.data, '$.tokens.total')
       END AS legacy_total
FROM %s m JOIN %s s ON s.id = m.session_id
LEFT JOIN message legacy ON legacy.id = m.id
WHERE m.type = 'assistant' AND json_valid(m.data)
ORDER BY m.id`

// messageData is the narrow slice of OpenCode's message.data JSON this
// provider reads. OpenCode's schema has changed across versions, so every
// field is optional: a missing field decodes to its zero value, never an
// error, and any field not listed here (prompt text, tool output, ...) is
// simply never unmarshaled - it cannot reach a model.UsageEvent.
type messageData struct {
	ModelID    string `json:"modelID"`
	ProviderID string `json:"providerID"`
	Model      struct {
		ID         string `json:"id"`
		ProviderID string `json:"providerID"`
	} `json:"model"`
	Time struct {
		Created int64 `json:"created"`
	} `json:"time"`
	Tokens struct {
		// Total is a pointer because its absence (673/18423 real assistant
		// records) and its presence-as-zero are different signals: absence
		// means "no arithmetic to check reasoningNested against", so the
		// nesting decision must default to additive rather than compare
		// against a fabricated 0.
		Total  *int64 `json:"total"`
		Input  int64  `json:"input"`
		Output int64  `json:"output"`
		Cache  struct {
			Read  int64 `json:"read"`
			Write int64 `json:"write"`
		} `json:"cache"`
		Reasoning int64 `json:"reasoning"`
	} `json:"tokens"`
	Cost float64 `json:"cost"`
}

// modelID returns the model identifier from either known on-disk generation.
// This is intentionally a closed struct, rather than a map or reflection: in
// particular, the v2 content field is not part of messageData.
func (d messageData) modelID() string {
	if d.Model.ID != "" {
		return d.Model.ID
	}
	return d.ModelID
}

// schemaQuery selects a current schema by its columns, not its table names.
// OpenCode leaves old tables behind during migrations, so falling back to
// legacy merely because a future generation is not called session_v2 would
// publish stale history. A current message table additionally has seq:
// session_inbox is deliberately close to a message table (id, session_id,
// type and data) but it is a delivery queue, not usage. Candidates are tried
// as pairs because independently selecting the newest table of each family
// can join two schema generations and quietly return no events.
//
// Residual risk: this selection rests on the NAME of one column. OpenCode's
// delivery queues already carry a "*_seq" column (session_inbox.enqueued_seq,
// session_pending.admitted_seq). A queue table that gains a column literally
// named "seq" would satisfy both hasColumns and schemaHasAssistantRows,
// because a queue row really does say type='assistant' and really does join
// a session. Measured today: no other table has id+session_id+type+seq+data,
// so this does not fire now.
func schemaQuery(ctx context.Context, db *sql.DB) (string, error) {
	tables, err := db.QueryContext(ctx, `
SELECT name, rowid FROM sqlite_master
WHERE type = 'table'
ORDER BY rowid DESC`)
	if err != nil {
		return "", err
	}
	defer tables.Close()

	type candidate struct {
		name  string
		rowID int64
	}
	var sessions, messages []candidate
	var legacyMessage bool
	var legacySession bool
	for tables.Next() {
		var table candidate
		if err := tables.Scan(&table.name, &table.rowID); err != nil {
			return "", err
		}
		columns, err := tableColumns(ctx, db, table.name)
		if err != nil {
			return "", err
		}
		if table.name == "message" && hasColumns(columns, "id", "session_id", "data") {
			legacyMessage = true
		}
		if table.name == "session" && hasColumns(columns, "id", "directory") {
			legacySession = true
		}
		if hasColumns(columns, "id", "directory") {
			sessions = append(sessions, table)
		}
		if hasColumns(columns, "id", "session_id", "type", "seq", "data") {
			messages = append(messages, table)
		}
	}
	if err := tables.Err(); err != nil {
		return "", err
	}
	if len(sessions) == 0 || len(messages) == 0 {
		return legacyQuery, nil
	}
	for _, message := range messages {
		for _, session := range sessions {
			query := currentSchemaQuery(message.name, session.name, legacyMessage)
			ok, err := schemaHasAssistantRows(ctx, db, message.name, session.name)
			if err != nil {
				return "", err
			}
			if ok {
				return query, nil
			}
		}
	}
	// A current-shaped generation does not become authoritative until it holds
	// an assistant message. A partially-created migration can contain newer
	// user rows while its legacy tables are still the only source of usage.
	// Select legacy only when it has an assistant row too: if neither family
	// does, preserve the empty current snapshot.
	if legacySession && legacyMessage {
		hasAssistant, err := legacyHasAssistantRows(ctx, db)
		if err != nil {
			return "", err
		}
		if hasAssistant {
			return legacyQuery, nil
		}
	}
	return currentSchemaQuery(messages[0].name, sessions[0].name, legacyMessage), nil
}

func currentSchemaQuery(messageTable, sessionTable string, hasLegacyMessage bool) string {
	if hasLegacyMessage {
		return fmt.Sprintf(currentWithLegacyMetadataQuery, quoteIdentifier(messageTable), quoteIdentifier(sessionTable))
	}
	return fmt.Sprintf(currentQuery, quoteIdentifier(messageTable), quoteIdentifier(sessionTable))
}

// legacyHasAssistantRows verifies that the legacy pair, rather than merely
// its table shape, contains usage. This deliberately projects no data blob.
func legacyHasAssistantRows(ctx context.Context, db *sql.DB) (bool, error) {
	var one int
	err := db.QueryRowContext(ctx, `
SELECT 1
FROM message m JOIN session s ON s.id = m.session_id
WHERE json_valid(m.data) AND json_extract(m.data, '$.role') = 'assistant'
LIMIT 1`).Scan(&one)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, err
}

// schemaHasAssistantRows verifies a candidate table pair before it becomes
// authoritative. The projected fields intentionally match currentQuery's
// relationship and assistant discriminator, but never read message.data.
func schemaHasAssistantRows(ctx context.Context, db *sql.DB, messageTable, sessionTable string) (bool, error) {
	query := fmt.Sprintf(`
SELECT 1
FROM %s m JOIN %s s ON s.id = m.session_id
WHERE m.type = 'assistant'
LIMIT 1`, quoteIdentifier(messageTable), quoteIdentifier(sessionTable))
	var one int
	err := db.QueryRowContext(ctx, query).Scan(&one)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, err
}

func tableColumns(ctx context.Context, db *sql.DB, table string) (map[string]struct{}, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+quoteIdentifier(table)+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := make(map[string]struct{})
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns[name] = struct{}{}
	}
	return columns, rows.Err()
}

func hasColumns(columns map[string]struct{}, required ...string) bool {
	for _, column := range required {
		if _, ok := columns[column]; !ok {
			return false
		}
	}
	return true
}

func quoteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

// reasoningNested reports whether arithmetic from the authoritative
// message's matching legacy metadata proves reasoning is inside output. The
// old generation never supplies an event; its declared total is evidence
// only. Missing metadata or arithmetic that does not match defaults to
// additive, and the rule deliberately has no model allowlist.
func (d messageData) reasoningNested(total *int64) bool {
	t := d.Tokens
	return total != nil && *total == t.Input+t.Output+t.Cache.Read+t.Cache.Write && t.Reasoning > 0
}

// Parse reads a single opencode.db Source read-only and emits one
// model.UsageEvent per assistant message. A row whose data isn't valid JSON
// is skipped, not fatal, so one corrupt message never aborts the scan.
//
// The native "cost" field is decoded (see messageData.Cost) but model core
// has no field to carry it on model.UsageEvent yet - that struct lives
// outside this ticket's file list. Reconciling it against the pricing
// catalog is JCB-313's job; for now the value is parsed and then dropped.
func (openCodeProvider) Parse(ctx context.Context, src provider.Source, emit func(model.UsageEvent) error) error {
	db, err := sql.Open("sqlite", DSN(src.Path))
	if err != nil {
		return privacy.Err(toolID, filepath.Base(src.Path), 0, err)
	}
	defer db.Close()

	query, err := schemaQuery(ctx, db)
	if err != nil {
		return privacy.Err(toolID, filepath.Base(src.Path), 0, err)
	}
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return privacy.Err(toolID, filepath.Base(src.Path), 0, err)
	}
	defer rows.Close()

	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}

		var id, sessionID, data, directory string
		var toolCalls int64
		var legacyID sql.NullString
		var legacyTotal sql.NullInt64
		if err := rows.Scan(&id, &sessionID, &data, &directory, &toolCalls, &legacyID, &legacyTotal); err != nil {
			return privacy.Err(toolID, filepath.Base(src.Path), 0, err)
		}

		var md messageData
		if err := json.Unmarshal([]byte(data), &md); err != nil {
			continue
		}

		var total *int64
		if legacyID.Valid && legacyTotal.Valid {
			total = &legacyTotal.Int64
		}
		output := md.Tokens.Output
		if md.reasoningNested(total) {
			output = max(0, output-md.Tokens.Reasoning)
		}

		evt := model.UsageEvent{
			Key: model.EventKey{
				Tool:      toolID,
				SessionID: sessionID,
				MessageID: id,
			},
			Tool:      toolID,
			Model:     md.modelID(),
			ProjectID: directory,
			Role:      "assistant",
			ToolCalls: toolCalls,
			Timestamp: time.UnixMilli(md.Time.Created).UTC(),
			Tokens: map[model.TokenClass]int64{
				model.TokenInput:      md.Tokens.Input,
				model.TokenOutput:     output,
				model.TokenCacheRead:  md.Tokens.Cache.Read,
				model.TokenCacheWrite: md.Tokens.Cache.Write,
				model.TokenReasoning:  md.Tokens.Reasoning,
			},
		}
		if err := emit(evt); err != nil {
			return err
		}
	}

	if err := rows.Err(); err != nil {
		return privacy.Err(toolID, filepath.Base(src.Path), 0, err)
	}
	return nil
}
