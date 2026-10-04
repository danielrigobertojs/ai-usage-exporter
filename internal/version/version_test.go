// SPDX-License-Identifier: Apache-2.0

package version

import (
	"runtime"
	"strings"
	"testing"
)

func TestString(t *testing.T) {
	origVersion, origCommit, origBuildDate := Version, Commit, BuildDate
	defer func() {
		Version, Commit, BuildDate = origVersion, origCommit, origBuildDate
	}()

	Version = "v1.2.3"
	Commit = "abc1234"
	BuildDate = "2026-10-04T00:00:00Z"

	got := String()

	for _, want := range []string{"ai-usage-exporter", "v1.2.3", "abc1234", "2026-10-04T00:00:00Z", runtime.Version()} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, want substring %q", got, want)
		}
	}
}

func TestStringDefaults(t *testing.T) {
	if Version == "" || Commit == "" || BuildDate == "" {
		t.Fatal("default build metadata vars must not be empty")
	}
	if !strings.HasPrefix(String(), "ai-usage-exporter ") {
		t.Errorf("String() = %q, want prefix %q", String(), "ai-usage-exporter ")
	}
}
