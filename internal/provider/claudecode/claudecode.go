// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

// Package claudecode implements the Claude Code provider: it parses
// ~/.claude/projects/<project-slug>/<session-uuid>.jsonl (relocatable via
// CLAUDE_CONFIG_DIR) into model.UsageEvent values.
//
// Claude Code auto-deletes session files after 30 days (configurable), so
// this provider's "all" window is never "the tool's lifetime history" - it
// is only whatever still happens to exist on disk at scan time. See
// docs/providers/claude-code.md and ADR-001 for why that makes gauges, not
// counters, the only metric type that can model it safely.
package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider"
)

// toolID is the `tool` label value for every event this provider emits -
// a public contract once shipped, per provider.Descriptor.ID.
const toolID = "claude-code"

// New returns the Claude Code provider.
func New() provider.Provider {
	return claudeCode{}
}

type claudeCode struct{}

func (claudeCode) Descriptor() provider.Descriptor {
	return provider.Descriptor{
		ID:          toolID,
		DisplayName: "Claude Code",
		Kind:        provider.SourceJSONL,
		HomeEnv:     []string{"CLAUDE_CONFIG_DIR"},
		Roots: []provider.RootSpec{
			{Base: provider.BaseHome, Rel: ".claude/projects", Glob: "*/*.jsonl"},
		},
		Capabilities: provider.Capabilities{
			HasTokens:      true,
			HasCacheTokens: true,
			HasToolCalls:   true,
			HasNativeCost:  false,
		},
	}
}

// entry is the bounded shape of one JSONL line this provider decodes. It
// carries ONLY the fields the field-mapping table in JCB-310 names - never
// a map[string]any over the full line - so there is a structural guarantee,
// not just a convention, that prompt or tool content can never reach a
// UsageEvent: the fields that would hold it (message text, tool_use
// input/output) simply have no struct field to decode into.
type entry struct {
	Type      string   `json:"type"`
	UUID      string   `json:"uuid"`
	SessionID string   `json:"sessionId"`
	CWD       string   `json:"cwd"`
	Timestamp string   `json:"timestamp"`
	Message   *message `json:"message"`
}

type message struct {
	Model   string         `json:"model"`
	Usage   *usage         `json:"usage"`
	Content []contentBlock `json:"content"`
}

type usage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}

// contentBlock decodes only the block-type discriminator: counting
// tool_use blocks never requires looking at a block's text or tool
// input/output.
type contentBlock struct {
	Type string `json:"type"`
}

// Parse implements provider.Provider. Only "assistant" entries carrying a
// usage block are billing-relevant; user entries, tool-result entries, and
// compaction summaries are skipped before being fully decoded.
func (claudeCode) Parse(ctx context.Context, src provider.Source, emit func(model.UsageEvent) error) error {
	f, err := os.Open(src.Path)
	if err != nil {
		return fmt.Errorf("claudecode: open %s: %w", src.Path, err)
	}
	defer f.Close()

	fallbackSession := sessionIDFromPath(src.Path)
	br := bufio.NewReaderSize(f, 64<<10)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		line, rerr := provider.ReadJSONLLine(br)
		if line != nil {
			if err := parseAndEmit(line, fallbackSession, src.Path, emit); err != nil {
				return err
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				return nil
			}
			return fmt.Errorf("claudecode: read %s: %w", src.Path, rerr)
		}
	}
}

// parseAndEmit decodes one already-bounded line and emits the UsageEvent it
// describes, if any. A line that fails to decode, isn't a usage-bearing
// assistant entry, or carries an unparseable timestamp is skipped: it is
// never treated as fatal to the rest of the file.
func parseAndEmit(line []byte, fallbackSession, srcPath string, emit func(model.UsageEvent) error) error {
	var e entry
	if err := json.Unmarshal(line, &e); err != nil {
		return nil
	}
	if e.Type != "assistant" || e.Message == nil || e.Message.Usage == nil {
		return nil
	}

	ts, err := time.Parse(time.RFC3339, e.Timestamp)
	if err != nil {
		return nil
	}

	sessionID := e.SessionID
	if sessionID == "" {
		sessionID = fallbackSession
	}

	var toolCalls int64
	for _, block := range e.Message.Content {
		if block.Type == "tool_use" {
			toolCalls++
		}
	}

	evt := model.UsageEvent{
		Key: model.EventKey{
			Tool:      toolID,
			SessionID: sessionID,
			MessageID: e.UUID,
		},
		Tool:      toolID,
		Model:     e.Message.Model,
		ProjectID: projectID(e.CWD, srcPath),
		Role:      e.Type,
		Timestamp: ts.UTC(),
		Tokens: map[model.TokenClass]int64{
			model.TokenInput:      e.Message.Usage.InputTokens,
			model.TokenOutput:     e.Message.Usage.OutputTokens,
			model.TokenCacheRead:  e.Message.Usage.CacheReadInputTokens,
			model.TokenCacheWrite: e.Message.Usage.CacheCreationInputTokens,
		},
		ToolCalls: toolCalls,
	}

	return emit(evt)
}

// sessionIDFromPath falls back to the session file's own name (without
// extension) when an entry carries no sessionId of its own.
func sessionIDFromPath(srcPath string) string {
	base := filepath.Base(srcPath)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// projectID normalizes cwd into the opt-in project label value, falling
// back to the session file's parent directory name - the project slug
// Claude Code itself encodes there - when an entry has no cwd of its own.
func projectID(cwd, srcPath string) string {
	if cwd != "" {
		return filepath.ToSlash(filepath.Clean(cwd))
	}
	return filepath.Base(filepath.Dir(srcPath))
}
