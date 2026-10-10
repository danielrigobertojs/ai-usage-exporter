// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

// Package privacy contains the small, auditable boundary between untrusted
// agent log data and diagnostics exposed by the exporter.
package privacy

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

var processSalt = newSalt()

func newSalt() [32]byte {
	var salt [32]byte
	if _, err := rand.Read(salt[:]); err != nil {
		panic("privacy: cannot initialize process salt")
	}
	return salt
}

// Err returns a parse error whose text contains only an approved provider,
// a provider-relative path, line number, and a safe cause description. It
// retains cause for errors.Is/errors.As without rendering arbitrary error
// strings (which may contain a log line or an absolute user path).
func Err(provider, relPath string, line int, cause error) error {
	return parseError{provider: provider, relPath: relPath, line: line, cause: cause}
}

type parseError struct {
	provider string
	relPath  string
	line     int
	cause    error
}

func (e parseError) Error() string {
	where := fmt.Sprintf("provider=%s path=%s", e.provider, e.relPath)
	if e.line > 0 {
		where += fmt.Sprintf(" line=%d", e.line)
	}
	return "parse error " + where + ": " + safeCause(e.cause)
}

func (e parseError) Unwrap() error { return e.cause }

func safeCause(err error) string {
	switch {
	case errors.Is(err, io.ErrUnexpectedEOF):
		return io.ErrUnexpectedEOF.Error()
	case errors.Is(err, io.EOF):
		return io.EOF.Error()
	case errors.Is(err, context.Canceled):
		return "context canceled"
	}
	if err == nil {
		return "unknown"
	}
	return fmt.Sprintf("%T", err)
}

// RelPath converts abs to a slash-separated path relative to root. It also
// recognizes Windows backslashes when the exporter is tested on another OS.
// Paths outside root, including sibling-prefix traps, are represented safely.
func RelPath(root, abs string) string {
	clean := func(s string) string { return path.Clean(strings.ReplaceAll(s, "\\", "/")) }
	r, a := clean(root), clean(abs)
	if r == "." || a == "." {
		return "<outside>"
	}
	if a == r {
		return "."
	}
	prefix := r
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	if !strings.HasPrefix(a, prefix) {
		return "<outside>"
	}
	rel := strings.TrimPrefix(a, prefix)
	if rel == "" || rel == "." || strings.HasPrefix(rel, "../") {
		return "<outside>"
	}
	return rel
}

// Fingerprint returns a process-local stable correlation identifier without
// exposing the input or allowing correlation across exporter restarts.
func Fingerprint(s string) string {
	h := sha256.New()
	_, _ = h.Write(processSalt[:])
	_, _ = h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))[:12]
}
