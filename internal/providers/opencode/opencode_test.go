// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package opencode

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	_ "modernc.org/sqlite"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/providers/opencode/testdata"
)

// TestDescriptorValid proves the descriptor this provider ships is
// well-formed enough for Discover to scan, per docs/adding-a-provider.md
// step 1.
func TestDescriptorValid(t *testing.T) {
	if err := New().Descriptor().Validate(); err != nil {
		t.Fatalf("Descriptor().Validate(): %v", err)
	}
}

// TestDSN covers step 2: DSN's exact shape is an acceptance criterion, not
// an implementation detail - mode=ro, query_only(1), and busy_timeout(2000)
// must all be present verbatim.
func TestDSN(t *testing.T) {
	got := DSN("/x/opencode.db")
	want := "file:/x/opencode.db?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(2000)"
	if got != want {
		t.Errorf("DSN(%q) = %q, want %q", "/x/opencode.db", got, want)
	}
}

func buildFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path, err := testdata.Build(dir, testdata.DefaultSessions, testdata.DefaultMessages)
	if err != nil {
		t.Fatalf("testdata.Build: %v", err)
	}
	return path
}

func parseAll(t *testing.T, path string) []model.UsageEvent {
	t.Helper()
	var events []model.UsageEvent
	src := provider.Source{Path: path, Kind: provider.SourceSQLite}
	err := New().Parse(context.Background(), src, func(e model.UsageEvent) error {
		events = append(events, e)
		return nil
	})
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	return events
}

// TestParseEmitsOnlyAssistantMessages covers step 4: Parse over the fixture
// emits exactly the 5 assistant-role events (the 1 user-role message and the
// 1 invalid-JSON message are excluded), each with the right SessionID,
// Model, ProjectID and all five TokenClass values.
func TestParseEmitsOnlyAssistantMessages(t *testing.T) {
	events := parseAll(t, buildFixture(t))

	// msg_4 (session ses_beta) is invalid JSON and must be skipped, so only
	// 4 of the 5 assistant rows in the fixture survive as events.
	if len(events) != 4 {
		t.Fatalf("len(events) = %d, want 4", len(events))
	}

	byID := make(map[string]model.UsageEvent, len(events))
	for _, e := range events {
		byID[e.Key.MessageID] = e
	}

	want := map[string]struct {
		sessionID string
		model     string
		projectID string
		tokens    map[model.TokenClass]int64
	}{
		"msg_1": {
			sessionID: "ses_alpha", model: "claude-sonnet-4-5", projectID: "/home/user/projects/alpha",
			tokens: map[model.TokenClass]int64{
				model.TokenInput: 100, model.TokenOutput: 50,
				model.TokenCacheRead: 10, model.TokenCacheWrite: 5, model.TokenReasoning: 0,
			},
		},
		"msg_2": {
			sessionID: "ses_alpha", model: "claude-sonnet-4-5", projectID: "/home/user/projects/alpha",
			tokens: map[model.TokenClass]int64{
				model.TokenInput: 200, model.TokenOutput: 75,
				model.TokenCacheRead: 0, model.TokenCacheWrite: 0, model.TokenReasoning: 3,
			},
		},
		"msg_3": {
			sessionID: "ses_beta", model: "gpt-5-codex", projectID: "/home/user/projects/beta",
			tokens: map[model.TokenClass]int64{
				model.TokenInput: 300, model.TokenOutput: 120,
				model.TokenCacheRead: 40, model.TokenCacheWrite: 0, model.TokenReasoning: 10,
			},
		},
		"msg_5": {
			sessionID: "ses_beta", model: "gpt-5-codex", projectID: "/home/user/projects/beta",
			tokens: map[model.TokenClass]int64{
				model.TokenInput: 15, model.TokenOutput: 5,
				model.TokenCacheRead: 0, model.TokenCacheWrite: 0, model.TokenReasoning: 0,
			},
		},
	}

	if len(byID) != len(want) {
		t.Fatalf("got message ids %v, want keys %v", keysOf(byID), keysOf(want))
	}

	for id, w := range want {
		e, ok := byID[id]
		if !ok {
			t.Errorf("missing event for message %q", id)
			continue
		}
		if e.Key.Tool != toolID || e.Tool != toolID {
			t.Errorf("%s: Tool = %q/%q, want %q", id, e.Key.Tool, e.Tool, toolID)
		}
		if e.Key.SessionID != w.sessionID {
			t.Errorf("%s: SessionID = %q, want %q", id, e.Key.SessionID, w.sessionID)
		}
		if e.Model != w.model {
			t.Errorf("%s: Model = %q, want %q", id, e.Model, w.model)
		}
		if e.ProjectID != w.projectID {
			t.Errorf("%s: ProjectID = %q, want %q", id, e.ProjectID, w.projectID)
		}
		if e.Role != "assistant" {
			t.Errorf("%s: Role = %q, want %q", id, e.Role, "assistant")
		}
		for class, wantCount := range w.tokens {
			if got := e.Tokens[class]; got != wantCount {
				t.Errorf("%s: Tokens[%s] = %d, want %d", id, class, got, wantCount)
			}
		}
	}
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestParseSchemaTolerance covers step 6: a message whose data has no
// tokens.cache object at all still emits an event with CacheRead/CacheWrite
// == 0 and no error (msg_2), and an invalid-JSON row is skipped while the
// rest of the file keeps parsing (msg_4, already exercised by the count in
// TestParseEmitsOnlyAssistantMessages).
func TestParseSchemaTolerance(t *testing.T) {
	events := parseAll(t, buildFixture(t))

	var got *model.UsageEvent
	for i := range events {
		if events[i].Key.MessageID == "msg_2" {
			got = &events[i]
		}
	}
	if got == nil {
		t.Fatal("msg_2 not found among emitted events")
	}
	if got.Tokens[model.TokenCacheRead] != 0 || got.Tokens[model.TokenCacheWrite] != 0 {
		t.Errorf("msg_2 Tokens = %+v, want CacheRead/CacheWrite == 0", got.Tokens)
	}
}

// TestParseNoSentinelPromptText guards the privacy invariant from
// docs/adding-a-provider.md: every fixture message's data carries the
// sentinel string in a field Parse never decodes, so it must never surface
// on any emitted event.
func TestParseNoSentinelPromptText(t *testing.T) {
	events := parseAll(t, buildFixture(t))
	for _, e := range events {
		if strings.Contains(e.Model, "SENTINEL-PROMPT-TEXT") ||
			strings.Contains(e.ProjectID, "SENTINEL-PROMPT-TEXT") ||
			strings.Contains(e.Role, "SENTINEL-PROMPT-TEXT") ||
			strings.Contains(e.Key.MessageID, "SENTINEL-PROMPT-TEXT") ||
			strings.Contains(e.Key.SessionID, "SENTINEL-PROMPT-TEXT") {
			t.Errorf("event %+v leaked the sentinel prompt text", e)
		}
	}
}

// TestParseReadOnly is the mandatory guarantee from step 7: Parse must never
// mutate opencode.db, even by a single byte, and must never leave behind a
// -wal file that wasn't there before.
func TestParseReadOnly(t *testing.T) {
	path := buildFixture(t)
	dir := filepath.Dir(path)

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile before Parse: %v", err)
	}
	beforeHash := sha256.Sum256(before)

	parseAll(t, path)

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after Parse: %v", err)
	}
	afterHash := sha256.Sum256(after)
	if beforeHash != afterHash {
		t.Error("opencode.db hash changed after Parse; Parse must be strictly read-only")
	}

	if _, err := os.Stat(path + "-wal"); err == nil {
		t.Error("Parse left behind an opencode.db-wal file")
	} else if !os.IsNotExist(err) {
		t.Fatalf("Stat %s-wal: %v", path, err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", dir, err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), "-wal") || strings.HasSuffix(entry.Name(), "-shm") {
			t.Errorf("Parse left behind a journal file: %s", entry.Name())
		}
	}
}

// TestParseNonexistentPath covers step 8: a path that doesn't exist on disk
// returns an error wrapped with this provider's identity, never a bare
// driver error and never a silent empty result.
func TestParseNonexistentPath(t *testing.T) {
	src := provider.Source{Path: filepath.Join(t.TempDir(), "missing.db"), Kind: provider.SourceSQLite}
	err := New().Parse(context.Background(), src, func(model.UsageEvent) error { return nil })
	if err == nil {
		t.Fatal("Parse on a nonexistent path: want error, got nil")
	}
	if !strings.Contains(err.Error(), "opencode:") {
		t.Errorf("Parse error = %q, want it to identify the opencode provider", err.Error())
	}
}

// TestDiscoverNoOpenCodeInstalled covers step 8's second half: Discover
// against a filesystem with no opencode.db anywhere returns zero sources
// and no error, exactly like a tool that was never installed.
func TestDiscoverNoOpenCodeInstalled(t *testing.T) {
	env := provider.Env{
		GOOS:   "linux",
		Home:   "home/user",
		Getenv: func(string) string { return "" },
		FS:     fstest.MapFS{},
	}
	sources, _, err := provider.Discover(context.Background(), New().Descriptor(), env, provider.DefaultBudget(time.Now()))
	if err != nil {
		t.Fatalf("Discover: unexpected error: %v", err)
	}
	if len(sources) != 0 {
		t.Errorf("len(sources) = %d, want 0", len(sources))
	}
}

// TestParseEmitErrorPropagates proves a failing emit callback aborts the
// scan and its error reaches the caller unwrapped-enough to compare with
// errors.Is, instead of being swallowed as a skip.
func TestParseEmitErrorPropagates(t *testing.T) {
	path := buildFixture(t)
	wantErr := errors.New("emit boom")
	src := provider.Source{Path: path, Kind: provider.SourceSQLite}

	err := New().Parse(context.Background(), src, func(model.UsageEvent) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Parse: err = %v, want %v", err, wantErr)
	}
}

// TestParseContextCancelledMidScan proves Parse stops as soon as ctx is
// cancelled between rows, instead of draining the rest of the result set.
func TestParseContextCancelledMidScan(t *testing.T) {
	path := buildFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src := provider.Source{Path: path, Kind: provider.SourceSQLite}

	calls := 0
	err := New().Parse(ctx, src, func(model.UsageEvent) error {
		calls++
		if calls == 1 {
			cancel()
		}
		return nil
	})
	if err == nil {
		t.Fatal("Parse with context cancelled mid-scan: want error, got nil")
	}
	if calls == 0 {
		t.Fatal("emit was never called before cancellation")
	}
}

// TestParseLockedDatabaseTimesOut covers step 9: a writer holding an
// exclusive lock on the database makes Parse return an error once
// busy_timeout(2000) elapses, rather than hanging forever.
func TestParseLockedDatabaseTimesOut(t *testing.T) {
	path := buildFixture(t)

	locker, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open locker connection: %v", err)
	}
	defer locker.Close()
	locker.SetMaxOpenConns(1)

	tx, err := locker.Begin()
	if err != nil {
		t.Fatalf("begin locking transaction: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("BEGIN EXCLUSIVE"); err == nil {
		// no-op: Begin() above already started a transaction; issue a
		// write to force SQLite to actually acquire the RESERVED/EXCLUSIVE
		// lock instead of staying in a lazily-started read transaction.
	}
	if _, err := tx.Exec("CREATE TABLE lock_holder (x INTEGER)"); err != nil {
		t.Fatalf("exec to acquire write lock: %v", err)
	}

	start := time.Now()
	src := provider.Source{Path: path, Kind: provider.SourceSQLite}
	parseErr := New().Parse(context.Background(), src, func(model.UsageEvent) error { return nil })
	elapsed := time.Since(start)

	if parseErr == nil {
		t.Fatal("Parse against a locked database: want error, got nil")
	}
	if elapsed > 5*time.Second {
		t.Errorf("Parse took %v to fail; busy_timeout(2000) should bound this well under 5s", elapsed)
	}
}
