// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package model

import (
	"strings"
	"testing"
	"time"
)

func TestUsageEventValid(t *testing.T) {
	validTime := time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		event   UsageEvent
		wantErr bool
	}{
		{
			name: "valid event with no tokens",
			event: UsageEvent{
				Tool:      "claude-code",
				Timestamp: validTime,
			},
			wantErr: false,
		},
		{
			name: "valid event with positive and zero tokens",
			event: UsageEvent{
				Tool:      "codex-cli",
				Timestamp: validTime,
				Tokens: map[TokenClass]int64{
					TokenInput:  100,
					TokenOutput: 0,
				},
			},
			wantErr: false,
		},
		{
			name: "empty tool",
			event: UsageEvent{
				Timestamp: validTime,
			},
			wantErr: true,
		},
		{
			name:    "zero timestamp",
			event:   UsageEvent{Tool: "opencode"},
			wantErr: true,
		},
		{
			name: "negative token count",
			event: UsageEvent{
				Tool:      "claude-code",
				Timestamp: validTime,
				Tokens: map[TokenClass]int64{
					TokenInput: -5,
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.event.Valid()
			if (err != nil) != tt.wantErr {
				t.Errorf("Valid() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNegativeTokenErrorMessage(t *testing.T) {
	event := UsageEvent{
		Tool:      "claude-code",
		Timestamp: time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC),
		Tokens:    map[TokenClass]int64{TokenCacheRead: -7},
	}

	err := event.Valid()
	if err == nil {
		t.Fatal("Valid() = nil, want negativeTokenError")
	}
	for _, want := range []string{"cache_read", "-7"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Valid() error = %q, want substring %q", err.Error(), want)
		}
	}
}
