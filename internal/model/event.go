// SPDX-License-Identifier: Apache-2.0

// Package model defines the normalized usage event every provider converges
// on before dedupe, window aggregation, and cost end up as shared logic
// instead of being reimplemented per tool.
package model

import (
	"errors"
	"strconv"
	"time"
)

// TokenClass categorizes a token count within a UsageEvent. It doubles as
// the token_type label value, so its set is the full cardinality budget for
// that label - see docs/metrics.md.
type TokenClass string

const (
	TokenInput      TokenClass = "input"
	TokenOutput     TokenClass = "output"
	TokenCacheRead  TokenClass = "cache_read"
	TokenCacheWrite TokenClass = "cache_write"
	TokenReasoning  TokenClass = "reasoning"
)

// EventKey identifies a usage event stably across scans and across
// reappearances of the same message (compaction, /resume, session forks),
// so the aggregator can dedupe by identity instead of by log line.
type EventKey struct {
	Tool      string
	SessionID string
	MessageID string
}

// UsageEvent is a normalized unit of usage. It carries metadata only: no
// field on this struct may ever hold prompt text, model output, or tool
// output - see the privacy invariant in the project instructions.
type UsageEvent struct {
	Key       EventKey
	Tool      string
	Model     string               // raw model id as reported by the tool; "" if unknown
	ProjectID string               // normalized cwd; only becomes a label if opted in
	Role      string               // "assistant" | "user" | "tool" | ""
	Timestamp time.Time            // always UTC
	Tokens    map[TokenClass]int64 // deltas, never cumulative totals
	ToolCalls int64
}

var (
	errEmptyTool = errors.New("model: tool is empty")
	errZeroTime  = errors.New("model: timestamp is zero")
)

// negativeTokenError reports a negative token count without pulling in fmt:
// fmt transitively imports os, which would break the package-purity
// invariant (no os/net/database/sql, not even transitively) checked by
// aggregate.TestPurityNoForbiddenDeps.
type negativeTokenError struct {
	class TokenClass
	count int64
}

func (e negativeTokenError) Error() string {
	return "model: negative token count for " + string(e.class) + ": " + strconv.FormatInt(e.count, 10)
}

// Valid reports whether e is well-formed enough to aggregate: Tool and
// Timestamp must be set, and no token count may be negative.
func (e UsageEvent) Valid() error {
	if e.Tool == "" {
		return errEmptyTool
	}
	if e.Timestamp.IsZero() {
		return errZeroTime
	}
	for class, count := range e.Tokens {
		if count < 0 {
			return negativeTokenError{class: class, count: count}
		}
	}
	return nil
}
