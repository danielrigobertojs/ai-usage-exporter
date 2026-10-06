// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package collector

// DefaultMaxProjectLabel is the cap applied to a project label value when
// Options.MaxProjectLabel is left at its zero value - see docs/metrics.md's
// cardinality budget.
const DefaultMaxProjectLabel = 48

// forbiddenLabelNames is the never-emit list: session identity is exactly
// the cardinality trap docs/metrics.md calls out, so it is enforced here as
// code, not only as a rule in prose.
var forbiddenLabelNames = map[string]bool{
	"session":    true,
	"session_id": true,
}

// NormalizeProjectLabel reduces a raw on-disk project identifier to a value
// safe to use as the opt-in project label. It returns ok == false whenever
// the label must not be emitted at all - the caller is disabled, or raw is
// empty - so a disabled Options.ProjectLabel can never leak a project value
// by accident. When enabled, the result is truncated to max characters (or
// DefaultMaxProjectLabel if max <= 0); the full on-disk path never reaches
// a label, truncated or not.
func NormalizeProjectLabel(raw string, enabled bool, max int) (value string, ok bool) {
	if !enabled || raw == "" {
		return "", false
	}
	if max <= 0 {
		max = DefaultMaxProjectLabel
	}
	if len(raw) > max {
		raw = raw[:max]
	}
	return raw, true
}
