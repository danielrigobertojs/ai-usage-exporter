// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

// Package opencode implements the provider.Provider for OpenCode, the only
// supported tool that keeps its history in SQLite instead of JSONL:
// ~/.local/share/opencode/opencode.db, tables session/message, with tokens
// and native cost living inside message.data's JSON blob.
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
	"time"

	_ "modernc.org/sqlite"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
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
const query = `
SELECT m.id, m.session_id, m.data, s.directory
FROM message m JOIN session s ON s.id = m.session_id
WHERE json_valid(m.data) AND json_extract(m.data, '$.role') = 'assistant'
ORDER BY m.id`

// messageData is the narrow slice of OpenCode's message.data JSON this
// provider reads. OpenCode's schema has changed across versions, so every
// field is optional: a missing field decodes to its zero value, never an
// error, and any field not listed here (prompt text, tool output, ...) is
// simply never unmarshaled - it cannot reach a model.UsageEvent.
type messageData struct {
	ModelID    string `json:"modelID"`
	ProviderID string `json:"providerID"`
	Time       struct {
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

// reasoningNested reports whether this record's own arithmetic shows
// reasoning tokens counted twice: once inside tokens.output and once inside
// tokens.reasoning. Per ADR-004, OpenCode does not use one convention across
// models - opencode-go/kimi-k2.5 nests reasoning inside output, every other
// provider/model pair observed on real data is additive - so the decision
// is derived from the record's own declared total, never a hardcoded model
// list. When total is absent there is no arithmetic to check, and the
// default is additive (false): 533/673 such records are reasoning>0 rows
// from opencode/grok-code, an additive pair in every record that does
// declare a total, and the remaining 140 have reasoning == 0 where the
// convention makes no difference.
func (d messageData) reasoningNested() bool {
	t := d.Tokens
	return t.Total != nil && *t.Total == t.Input+t.Output+t.Cache.Read+t.Cache.Write && t.Reasoning > 0
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
		return fmt.Errorf("opencode: open %s: %w", src.Path, err)
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("opencode: query %s: %w", src.Path, err)
	}
	defer rows.Close()

	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}

		var id, sessionID, data, directory string
		if err := rows.Scan(&id, &sessionID, &data, &directory); err != nil {
			return fmt.Errorf("opencode: scan %s: %w", src.Path, err)
		}

		var md messageData
		if err := json.Unmarshal([]byte(data), &md); err != nil {
			continue
		}

		output := md.Tokens.Output
		if md.reasoningNested() {
			output = max(0, output-md.Tokens.Reasoning)
		}

		evt := model.UsageEvent{
			Key: model.EventKey{
				Tool:      toolID,
				SessionID: sessionID,
				MessageID: id,
			},
			Tool:      toolID,
			Model:     md.ModelID,
			ProjectID: directory,
			Role:      "assistant",
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
		return fmt.Errorf("opencode: iterate %s: %w", src.Path, err)
	}
	return nil
}
