// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

// Package codex implements provider.Provider for OpenAI Codex CLI rollouts.
// See docs/providers/codex.md for the on-disk format and, in particular, the
// cumulative-counter semantics delta.go exists to undo.
package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider"
)

const toolID = "codex"

// scannerBufferSize bounds a single JSONL line. Codex turn lines are small;
// this only guards against a pathological line without risking an
// allocation anywhere near the file's full size (rollouts run 700 MB-2 GB).
const scannerBufferSize = 8 << 20

// New returns the Codex provider.
func New() provider.Provider {
	return codexProvider{}
}

type codexProvider struct{}

func (codexProvider) Descriptor() provider.Descriptor {
	return provider.Descriptor{
		ID:          toolID,
		DisplayName: "OpenAI Codex",
		Kind:        provider.SourceJSONL,
		HomeEnv:     []string{"CODEX_HOME"},
		Roots: []provider.RootSpec{
			{Base: provider.BaseHome, Rel: ".codex/sessions", Glob: "*/*/*/rollout-*.jsonl"},
			{Base: provider.BaseHome, Rel: ".codex/archived_sessions", Glob: "**/*.jsonl"},
		},
		Capabilities: provider.Capabilities{
			HasTokens:      true,
			HasCacheTokens: true,
			HasToolCalls:   true,
			HasNativeCost:  false,
		},
	}
}

// sessionMetaLine is the first line of a well-formed rollout: the only place
// the session id and cwd are recorded.
type sessionMetaLine struct {
	Type    string `json:"type"`
	Payload struct {
		ID  string `json:"id"`
		Cwd string `json:"cwd"`
	} `json:"payload"`
}

// turnLine is one turn event. LastInputTokens/LastOutputTokens/
// LastCachedTokens/LastTotalTokens are cumulative totals for the session's
// current context window, not deltas - see delta.go.
type turnLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Payload   struct {
		ID               string `json:"id"`
		Model            string `json:"model"`
		LastInputTokens  int64  `json:"last_input_tokens"`
		LastOutputTokens int64  `json:"last_output_tokens"`
		LastCachedTokens int64  `json:"last_cached_tokens"`
		LastTotalTokens  int64  `json:"last_total_tokens"`
		ToolCalls        int64  `json:"tool_calls"`
	} `json:"payload"`
}

// Parse reads one Codex rollout (or archived_sessions) JSONL file in
// streaming fashion and emits one UsageEvent per turn, with last_*
// cumulative counters converted into per-turn deltas by a Tracker scoped to
// this single file.
func (codexProvider) Parse(ctx context.Context, src provider.Source, emit func(model.UsageEvent) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	f, err := os.Open(src.Path)
	if err != nil {
		return fmt.Errorf("codex: open %s: %w", src.Path, err)
	}
	defer f.Close()

	sessionID := filepath.Base(src.Path)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), scannerBufferSize)

	tracker := NewTracker()
	first := true
	index := 0

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}

		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		if first {
			first = false
			var meta sessionMetaLine
			if err := json.Unmarshal(line, &meta); err == nil && meta.Type == "session_meta" && meta.Payload.ID != "" {
				sessionID = meta.Payload.ID
				continue
			}
			// Not a session_meta line (rollout_no_meta.jsonl, or a truncated
			// first write): fall through and try to parse it as a turn.
		}

		var turn turnLine
		if err := json.Unmarshal(line, &turn); err != nil {
			// A malformed or truncated line is not fatal: skip it and keep
			// scanning the rest of the file.
			continue
		}
		if turn.Type != "turn" {
			continue
		}

		ts, err := time.Parse(time.RFC3339, turn.Timestamp)
		if err != nil {
			continue
		}

		delta := tracker.Delta(Cumulative{
			Input:  turn.Payload.LastInputTokens,
			Output: turn.Payload.LastOutputTokens,
			Cached: turn.Payload.LastCachedTokens,
			Total:  turn.Payload.LastTotalTokens,
		})

		index++
		messageID := turn.Payload.ID
		if messageID == "" {
			messageID = fmt.Sprintf("%s#%d", sessionID, index)
		}

		evt := model.UsageEvent{
			Key: model.EventKey{
				Tool:      toolID,
				SessionID: sessionID,
				MessageID: messageID,
			},
			Tool:      toolID,
			Model:     turn.Payload.Model,
			Role:      "assistant",
			Timestamp: ts.UTC(),
			Tokens: map[model.TokenClass]int64{
				model.TokenInput:     delta.Input,
				model.TokenOutput:    delta.Output,
				model.TokenCacheRead: delta.Cached,
			},
			ToolCalls: turn.Payload.ToolCalls,
		}

		if err := emit(evt); err != nil {
			return err
		}
	}

	return scanner.Err()
}
