// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package privacy

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestErrDoesNotRenderArbitraryCauseText(t *testing.T) {
	const sentinel = "SENTINEL-PROMPT-TEXT-which-must-not-leak"
	err := Err("codex", "sessions/2026/03/15/rollout-x.jsonl", 42, errors.New(sentinel))
	got := err.Error()
	for _, want := range []string{"codex", "sessions/2026/03/15/rollout-x.jsonl", "line=42"} {
		if !strings.Contains(got, want) {
			t.Errorf("Error() = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, sentinel) {
		t.Errorf("Error() leaked content: %q", got)
	}
	if !errors.Is(Err("codex", "x", 1, io.ErrUnexpectedEOF), io.ErrUnexpectedEOF) {
		t.Error("Err does not preserve cause")
	}
	if !strings.Contains(Err("codex", "x", 1, io.ErrUnexpectedEOF).Error(), io.ErrUnexpectedEOF.Error()) {
		t.Error("safe I/O cause missing")
	}
}

func TestRelPath(t *testing.T) {
	for _, tt := range []struct{ root, abs, want string }{
		{"/home/u/.codex", "/home/u/.codex/sessions/a.jsonl", "sessions/a.jsonl"},
		{"/home/u/.codex", "/home/u/.codex-other/a.jsonl", "<outside>"},
		{`C:\\Users\\u\\.codex`, `C:\\Users\\u\\.codex\\sessions\\a.jsonl`, "sessions/a.jsonl"},
	} {
		if got := RelPath(tt.root, tt.abs); got != tt.want {
			t.Errorf("RelPath(%q, %q) = %q, want %q", tt.root, tt.abs, got, tt.want)
		}
	}
}

func TestFingerprintIsStableAndShort(t *testing.T) {
	if os.Getenv("PRIVACY_FINGERPRINT_HELPER") == "1" {
		fmt.Print(Fingerprint("secret"))
		os.Exit(0)
	}
	a, b := Fingerprint("secret"), Fingerprint("secret")
	if a != b {
		t.Errorf("Fingerprint is not stable: %q != %q", a, b)
	}
	if len(a) != 12 {
		t.Errorf("Fingerprint length = %d, want 12", len(a))
	}
	if a == Fingerprint("other") {
		t.Error("distinct inputs fingerprint identically")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestFingerprintIsStableAndShort$")
	cmd.Env = append(os.Environ(), "PRIVACY_FINGERPRINT_HELPER=1")
	other, err := cmd.Output()
	if err != nil {
		t.Fatalf("fingerprint subprocess: %v", err)
	}
	if got := strings.TrimSpace(string(other)); got == a {
		t.Errorf("Fingerprint was reused across processes: %q", got)
	}
}
