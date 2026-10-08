// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestExecuteVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if got := Execute([]string{"version"}, &out, &errOut); got != 0 {
		t.Fatalf("exit=%d", got)
	}
	if !strings.Contains(out.String(), "ai-usage-exporter") {
		t.Fatal("version missing")
	}
}
func TestProvidersJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	if got := Execute([]string{"providers", "--output", "json"}, &out, &errOut); got != 0 {
		t.Fatalf("exit=%d: %s", got, errOut.String())
	}
	var rows []providerView
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("providers=%d, want 3", len(rows))
	}
}
func TestReportRejectsWindow(t *testing.T) {
	var out, errOut bytes.Buffer
	if got := Execute([]string{"report", "--window", "bogus"}, &out, &errOut); got != 2 {
		t.Fatalf("exit=%d", got)
	}
	if out.Len() != 0 || !strings.Contains(errOut.String(), "24h") {
		t.Fatalf("out=%q err=%q", out.String(), errOut.String())
	}
}
func TestExecuteRequiresSubcommand(t *testing.T) {
	for _, args := range [][]string{nil, {"serve"}} {
		var out, errOut bytes.Buffer
		if got := Execute(args, &out, &errOut); got != 2 || errOut.Len() == 0 {
			t.Fatalf("args=%v exit=%d stderr=%q", args, got, errOut.String())
		}
	}
}
