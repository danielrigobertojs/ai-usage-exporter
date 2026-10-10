// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package e2e

import (
	"context"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/collector"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/config"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/pricing"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider/all"
	openfixture "github.com/danielrigobertojs/ai-usage-exporter/internal/provider/opencode/testdata"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/scan"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/server"
)

const sentinel = "SENTINEL-PROMPT-TEXT"

type rootFS struct{}

func (rootFS) Open(name string) (fs.File, error) { return os.Open("/" + name) }

func TestMetricsNeverExposeLogContentAfterRescan(t *testing.T) {
	home := t.TempDir()
	writePrivacyFixtures(t, home)
	reg, err := all.Registry()
	if err != nil {
		t.Fatal(err)
	}
	env := provider.Env{GOOS: "linux", Home: strings.TrimPrefix(filepath.ToSlash(home), "/"), Getenv: func(string) string { return "" }, FS: rootFS{}}
	for _, projectLabels := range []bool{false, true} {
		t.Run("project_labels", func(t *testing.T) {
			result, err := scan.Run(context.Background(), reg, env, provider.DefaultBudget(time.Now()), time.Now(), time.UTC)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			c := collector.New(pricing.Embedded(), collector.Options{ProjectLabel: projectLabels})
			c.Set(result)
			cfg, err := config.Load("", func(string) string { return "" }, nil)
			if err != nil {
				t.Fatal(err)
			}
			ts := httptest.NewServer(server.New(cfg, c, c.Ready).Handler)
			defer ts.Close()
			assertNoSentinel(t, ts.URL+"/metrics")
			// A second Run is the exact publication path used after SIGHUP.
			result, err = scan.Run(context.Background(), reg, env, provider.DefaultBudget(time.Now()), time.Now(), time.UTC)
			if err != nil {
				t.Fatalf("rescan: %v", err)
			}
			c.Set(result)
			assertNoSentinel(t, ts.URL+"/metrics")
		})
	}
}

func assertNoSentinel(t *testing.T, url string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), sentinel) {
		t.Fatalf("metrics leaked sentinel: %s", body)
	}
}

func writePrivacyFixtures(t *testing.T, home string) {
	t.Helper()
	claude := filepath.Join(home, ".claude", "projects", "project", "session.jsonl")
	codex := filepath.Join(home, ".codex", "sessions", "2026", "01", "01", "rollout-x.jsonl")
	if err := os.MkdirAll(filepath.Dir(claude), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(codex), 0o700); err != nil {
		t.Fatal(err)
	}
	claudeLine := `{"type":"assistant","uuid":"m","sessionId":"s","cwd":"` + sentinel + `","timestamp":"2026-01-01T00:00:00Z","message":{"model":"m","content":[{"type":"text","text":"` + sentinel + `"}],"usage":{"input_tokens":1,"output_tokens":1}}}` + "\n"
	codexLine := `{"type":"session_meta","payload":{"id":"s","cwd":"` + sentinel + `"}}` + "\n" + `{"type":"turn_context","payload":{"model":"m"}}` + "\n" + `{"type":"event_msg","timestamp":"2026-01-01T00:00:00Z","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2},"text":"` + sentinel + `"}}}` + "\n"
	if err := os.WriteFile(claude, []byte(claudeLine), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codex, []byte(codexLine), 0o600); err != nil {
		t.Fatal(err)
	}
	dbDir := filepath.Join(home, ".local", "share", "opencode")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := openfixture.Build(dbDir, []openfixture.Session{{ID: "s", Directory: sentinel}}, []openfixture.Message{{ID: "m", SessionID: "s", Data: `{"role":"assistant","modelID":"m","summary":"` + sentinel + `","time":{"created":1767225600000},"tokens":{"input":1,"output":1}}`}}); err != nil {
		t.Fatal(err)
	}
}
