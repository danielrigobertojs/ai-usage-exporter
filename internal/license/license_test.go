// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package license

import (
	"strings"
	"testing"
)

func TestConstantsNotEmpty(t *testing.T) {
	for name, v := range map[string]string{
		"Project":   Project,
		"Copyright": Copyright,
		"License":   License,
		"URL":       URL,
	} {
		if v == "" {
			t.Errorf("%s must not be empty", name)
		}
		if strings.ContainsAny(v, "<>") {
			t.Errorf("%s = %q still contains a placeholder bracket", name, v)
		}
	}
}

func TestAttribution(t *testing.T) {
	got := Attribution()

	for _, want := range []string{Project, Copyright, License, URL} {
		if !strings.Contains(got, want) {
			t.Errorf("Attribution() = %q, want substring %q", got, want)
		}
	}
}
