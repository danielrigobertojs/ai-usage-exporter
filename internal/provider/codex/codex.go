// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

// Package codex implements provider.Provider for OpenAI Codex CLI rollouts.
// See docs/providers/codex.md for the on-disk format - in particular, why
// usage travels in event_msg/token_count lines rather than the "turn" shape
// an earlier version of this package assumed, and why delta.go undoes two
// independent nestings, not one cumulative-vs-delta problem.
package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/privacy"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider"
)

const toolID = "codex"
const unknownModel = "unknown"

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

// rolloutLine is the bounded shape every line in a Codex rollout decodes
// into first. Payload stays raw until Type says which of the shapes below
// applies - this is also what keeps prompt/tool content structurally
// unreachable: only a handful of named leaf fields across all the payload
// shapes below are ever decoded, and none of them is message text, a tool
// argument, or a tool result.
type rolloutLine struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

// sessionMetaPayload is the payload of the first line of a well-formed
// rollout. Some files carry both id and session_id with the same value;
// id wins when both are present.
type sessionMetaPayload struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Model     string `json:"model"`
}

// turnContextPayload carries the model in effect for the turns that follow
// it. A rollout has one turn_context line per turn, so the model attached
// to a token_count line is whichever turn_context was last seen before it.
type turnContextPayload struct {
	Model string `json:"model"`
}

// eventMsgPayload is the payload of an event_msg line. Only the
// "token_count" sub-type carries usage; every other sub-type
// (task_started, task_complete, thread_settings_applied, ...) is ignored.
type eventMsgPayload struct {
	Type string         `json:"type"`
	Info tokenCountInfo `json:"info"`
}

// tokenCountInfo is the "info" object of a token_count event_msg.
// last_token_usage is deliberately not decoded: it repeats the full
// cumulative value verbatim on duplicate token_count emissions, so feeding
// it to Tracker as if it were a delta would double-count the duplicated
// turn. total_token_usage is the true session-wide running total and is
// what Tracker is fed from - see delta.go.
type tokenCountInfo struct {
	TotalTokenUsage tokenUsage `json:"total_token_usage"`
}

// tokenUsage is OpenAI's cumulative usage shape. CachedInputTokens nests
// inside InputTokens and ReasoningOutputTokens nests inside OutputTokens -
// see docs/providers/codex.md for the evidence. A cache_write_input_tokens
// field has been observed in real rollouts but is deliberately never
// decoded: Codex has no cache-write concept of its own (every occurrence
// observed is 0), and emitting a measured-looking 0 under
// model.TokenCacheWrite would hide that this class is simply unavailable
// for this provider, which is a different thing from a true zero.
type tokenUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
	TotalTokens           int64 `json:"total_tokens"`
}

// responseItemPayload decodes only the type discriminator of a
// response_item line, by design: counting tool calls never requires
// looking at payload.name or payload.arguments, both of which can carry
// real file paths and shell commands.
type responseItemPayload struct {
	Type string `json:"type"`
}

// toolCallResponseTypes are the response_item payload.type values that
// represent a tool invocation worth counting toward ToolCalls.
var toolCallResponseTypes = map[string]bool{
	"function_call":    true,
	"custom_tool_call": true,
	"web_search_call":  true,
}

// Parse reads one Codex rollout (or archived_sessions) JSONL file in
// streaming fashion and emits one UsageEvent per token_count event_msg
// line, with the session's cumulative total_token_usage converted into a
// per-turn delta by a Tracker scoped to this single file, and that delta's
// two nestings (cache-in-input, reasoning-in-output) undone before
// emission. See docs/providers/codex.md.
func (codexProvider) Parse(ctx context.Context, src provider.Source, emit func(model.UsageEvent) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	f, err := os.Open(src.Path)
	if err != nil {
		return privacy.Err(toolID, filepath.Base(src.Path), 0, err)
	}
	defer f.Close()

	sessionID := sessionIDFromFilename(src.Path)
	currentModel := ""
	pendingToolCalls := int64(0)
	tokenCountIndex := 0
	lineNumber := 0

	br := bufio.NewReaderSize(f, 64<<10)
	tracker := NewTracker()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		line, rerr := provider.ReadJSONLLine(br)
		if line != nil {
			lineNumber++
			var rl rolloutLine
			if err := json.Unmarshal(line, &rl); err == nil {
				switch rl.Type {
				case "session_meta":
					var p sessionMetaPayload
					if err := json.Unmarshal(rl.Payload, &p); err == nil {
						if p.ID != "" {
							sessionID = p.ID
						} else if p.SessionID != "" {
							sessionID = p.SessionID
						}
						if p.Model != "" {
							currentModel = p.Model
						}
					}

				case "turn_context":
					var p turnContextPayload
					if err := json.Unmarshal(rl.Payload, &p); err == nil && p.Model != "" {
						currentModel = p.Model
					}

				case "response_item":
					var p responseItemPayload
					if err := json.Unmarshal(rl.Payload, &p); err == nil && toolCallResponseTypes[p.Type] {
						pendingToolCalls++
					}

				case "event_msg":
					var p eventMsgPayload
					if err := json.Unmarshal(rl.Payload, &p); err == nil && p.Type == "token_count" {
						ts, terr := time.Parse(time.RFC3339, rl.Timestamp)
						if terr == nil {
							modelLabel := currentModel
							if modelLabel == "" {
								modelLabel = unknownModel
							}

							delta := tracker.Delta(Cumulative{
								Input:           p.Info.TotalTokenUsage.InputTokens,
								CachedInput:     p.Info.TotalTokenUsage.CachedInputTokens,
								Output:          p.Info.TotalTokenUsage.OutputTokens,
								ReasoningOutput: p.Info.TotalTokenUsage.ReasoningOutputTokens,
								Total:           p.Info.TotalTokenUsage.TotalTokens,
							})

							evt := model.UsageEvent{
								Key: model.EventKey{
									Tool:      toolID,
									SessionID: sessionID,
									MessageID: strconv.Itoa(tokenCountIndex),
								},
								Tool:      toolID,
								Model:     modelLabel,
								Role:      "assistant",
								Timestamp: ts.UTC(),
								Tokens: map[model.TokenClass]int64{
									model.TokenInput:     max(0, delta.Input-delta.CachedInput),
									model.TokenCacheRead: delta.CachedInput,
									model.TokenOutput:    max(0, delta.Output-delta.ReasoningOutput),
									model.TokenReasoning: delta.ReasoningOutput,
								},
								ToolCalls: pendingToolCalls,
							}

							if err := emit(evt); err != nil {
								return err
							}

							tokenCountIndex++
							pendingToolCalls = 0
						}
					}

				// "turn_context", "response_item" and "event_msg" are handled
				// above; everything else (world_state, token_usage_record -
				// one occurrence across the real rollouts this parser was
				// verified against, negligible volume - and any future
				// sub-type) is intentionally ignored rather than treated as
				// an error.
				default:
				}
			}
		}

		if rerr != nil {
			if rerr == io.EOF {
				return nil
			}
			return privacy.Err(toolID, filepath.Base(src.Path), lineNumber+1, rerr)
		}
	}
}

// sessionIDFromFilename falls back to the rollout file's own name (without
// extension) when the file has no session_meta line, or its first line is
// truncated or otherwise unreadable.
func sessionIDFromFilename(srcPath string) string {
	base := filepath.Base(srcPath)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
