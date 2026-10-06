// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package provider

import (
	"context"
	"io/fs"
	"reflect"
	"testing"
	"testing/fstest"
	"time"
)

func mustParse(t *testing.T, value string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("time.Parse(%q): %v", value, err)
	}
	return ts
}

// TestDiscoverSizeBudgetAndOrdering covers step 6: an oversized file is
// excluded and counted as skipped regardless of how recent it is, and the
// surviving sources come back ordered by ModTime descending.
func TestDiscoverSizeBudgetAndOrdering(t *testing.T) {
	tA := mustParse(t, "2026-01-01T00:00:00Z")
	tB := mustParse(t, "2026-01-02T00:00:00Z")
	tBig := mustParse(t, "2026-01-03T00:00:00Z") // most recent, but oversized

	mapFS := fstest.MapFS{
		"home/user/.claude/projects/proj/a.jsonl":   {Data: []byte("small-a"), ModTime: tA},
		"home/user/.claude/projects/proj/b.jsonl":   {Data: []byte("small-b"), ModTime: tB},
		"home/user/.claude/projects/proj/big.jsonl": {Data: []byte("eleven-bytes"), ModTime: tBig},
	}

	d := Descriptor{
		ID:   "x",
		Kind: SourceJSONL,
		Roots: []RootSpec{
			{Base: BaseHome, Rel: ".claude/projects", Glob: "*/*.jsonl"},
		},
	}
	env := Env{GOOS: "linux", Home: "home/user", Getenv: func(string) string { return "" }, FS: mapFS}
	budget := Budget{
		Deadline:      time.Now().Add(time.Hour),
		MaxFiles:      100,
		MaxBytesFile:  10, // "eleven-bytes" is 12 bytes, so it's the one that gets skipped
		MaxTotalBytes: 1 << 30,
	}

	sources, stats, err := Discover(context.Background(), d, env, budget)
	if err != nil {
		t.Fatalf("Discover: unexpected error: %v", err)
	}
	if stats.FilesSeen != 3 {
		t.Errorf("FilesSeen = %d, want 3", stats.FilesSeen)
	}
	if stats.FilesSkipped != 1 {
		t.Errorf("FilesSkipped = %d, want 1", stats.FilesSkipped)
	}
	if stats.BudgetHit {
		t.Error("BudgetHit = true, want false")
	}
	if len(sources) != 2 {
		t.Fatalf("len(sources) = %d, want 2", len(sources))
	}
	if sources[0].ModTime.Before(sources[1].ModTime) {
		t.Errorf("sources not ordered by ModTime descending: %v before %v", sources[0].ModTime, sources[1].ModTime)
	}
	if sources[0].ModTime != tB || sources[1].ModTime != tA {
		t.Errorf("sources = %+v, want b (%v) then a (%v)", sources, tB, tA)
	}
}

// TestDiscoverDeadlineExceeded covers step 7: a Budget whose Deadline has
// already passed yields BudgetHit=true and a (possibly empty) partial list,
// never an error.
func TestDiscoverDeadlineExceeded(t *testing.T) {
	mapFS := fstest.MapFS{
		"home/user/.claude/projects/proj/a.jsonl": {Data: []byte("x"), ModTime: mustParse(t, "2026-01-01T00:00:00Z")},
	}
	d := Descriptor{
		ID: "x",
		Roots: []RootSpec{
			{Base: BaseHome, Rel: ".claude/projects", Glob: "*/*.jsonl"},
		},
	}
	env := Env{GOOS: "linux", Home: "home/user", Getenv: func(string) string { return "" }, FS: mapFS}
	budget := Budget{
		Deadline:      time.Now().Add(-time.Hour), // already in the past
		MaxFiles:      100,
		MaxBytesFile:  1 << 30,
		MaxTotalBytes: 1 << 30,
	}

	sources, stats, err := Discover(context.Background(), d, env, budget)
	if err != nil {
		t.Fatalf("Discover: unexpected error: %v", err)
	}
	if !stats.BudgetHit {
		t.Error("BudgetHit = false, want true")
	}
	if len(sources) != 0 {
		t.Errorf("len(sources) = %d, want 0", len(sources))
	}
}

// TestDiscoverHomeEnvOverride covers step 8: when a HomeEnv variable is
// set, it relocates every root of that descriptor, and the roots under the
// ordinary (unset-override) home are never consulted at all.
func TestDiscoverHomeEnvOverride(t *testing.T) {
	mapFS := fstest.MapFS{
		"home/user/.tool/default.jsonl": {Data: []byte("x"), ModTime: mustParse(t, "2026-01-01T00:00:00Z")},
		"custom/.tool/data.jsonl":       {Data: []byte("x"), ModTime: mustParse(t, "2026-01-01T00:00:00Z")},
	}
	d := Descriptor{
		ID:      "x",
		HomeEnv: []string{"TOOL_HOME"},
		Roots: []RootSpec{
			{Base: BaseHome, Rel: ".tool", Glob: "*.jsonl"},
		},
	}
	env := Env{
		GOOS: "linux",
		Home: "home/user",
		Getenv: func(k string) string {
			if k == "TOOL_HOME" {
				return "custom"
			}
			return ""
		},
		FS: mapFS,
	}
	budget := DefaultBudget(time.Now())

	sources, _, err := Discover(context.Background(), d, env, budget)
	if err != nil {
		t.Fatalf("Discover: unexpected error: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("len(sources) = %d, want 1 (only the overridden home's file)", len(sources))
	}
	const want = "/custom/.tool/data.jsonl"
	if sources[0].Path != want {
		t.Errorf("sources[0].Path = %q, want %q", sources[0].Path, want)
	}
}

// TestDiscoverGOOSFilter proves a root whose GOOS doesn't match env.GOOS is
// excluded outright: its files never show up, not even as skipped.
func TestDiscoverGOOSFilter(t *testing.T) {
	mapFS := fstest.MapFS{
		"home/user/.win/x.jsonl": {Data: []byte("x"), ModTime: mustParse(t, "2026-01-01T00:00:00Z")},
		"home/user/.nix/y.jsonl": {Data: []byte("y"), ModTime: mustParse(t, "2026-01-01T00:00:00Z")},
	}
	d := Descriptor{
		ID: "x",
		Roots: []RootSpec{
			{GOOS: "windows", Base: BaseHome, Rel: ".win", Glob: "*.jsonl"},
			{Base: BaseHome, Rel: ".nix", Glob: "*.jsonl"},
		},
	}
	env := Env{GOOS: "linux", Home: "home/user", Getenv: func(string) string { return "" }, FS: mapFS}

	sources, stats, err := Discover(context.Background(), d, env, DefaultBudget(time.Now()))
	if err != nil {
		t.Fatalf("Discover: unexpected error: %v", err)
	}
	if stats.FilesSeen != 1 {
		t.Errorf("FilesSeen = %d, want 1 (the windows-only root must never be consulted)", stats.FilesSeen)
	}
	if len(sources) != 1 || sources[0].Path != "/home/user/.nix/y.jsonl" {
		t.Errorf("sources = %+v, want only the .nix file", sources)
	}
}

// TestDiscoverNonexistentRoot covers the acceptance criterion: a tool that
// isn't installed is not an error, just an empty result.
func TestDiscoverNonexistentRoot(t *testing.T) {
	d := Descriptor{
		ID: "not-installed",
		Roots: []RootSpec{
			{Base: BaseHome, Rel: ".not-installed", Glob: "*.jsonl"},
		},
	}
	env := Env{GOOS: "linux", Home: "home/user", Getenv: func(string) string { return "" }, FS: fstest.MapFS{}}

	sources, stats, err := Discover(context.Background(), d, env, DefaultBudget(time.Now()))
	if err != nil {
		t.Fatalf("Discover: unexpected error: %v", err)
	}
	if len(sources) != 0 {
		t.Errorf("len(sources) = %d, want 0", len(sources))
	}
	if stats.FilesSeen != 0 {
		t.Errorf("FilesSeen = %d, want 0", stats.FilesSeen)
	}
}

// TestDiscoverCancelledContext proves Discover refuses to start a scan
// against a context that's already done, instead of doing partial work.
func TestDiscoverCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	d := Descriptor{ID: "x", Roots: []RootSpec{{Base: BaseHome, Rel: ".x", Glob: "*.jsonl"}}}
	env := Env{GOOS: "linux", Home: "home/user", Getenv: func(string) string { return "" }, FS: fstest.MapFS{}}

	_, _, err := Discover(ctx, d, env, DefaultBudget(time.Now()))
	if err == nil {
		t.Fatal("Discover with a cancelled context: want error, got nil")
	}
}

// TestDiscoverInvalidDescriptor proves Discover rejects a malformed
// Descriptor instead of scanning against it.
func TestDiscoverInvalidDescriptor(t *testing.T) {
	d := Descriptor{ID: "Not-Kebab-Case"}
	env := Env{GOOS: "linux", Home: "home/user", Getenv: func(string) string { return "" }, FS: fstest.MapFS{}}

	_, _, err := Discover(context.Background(), d, env, DefaultBudget(time.Now()))
	if err == nil {
		t.Fatal("Discover with an invalid descriptor: want error, got nil")
	}
}

// TestDiscoverUnresolvableBase proves a root whose Base can't resolve (here,
// BaseHome with no Home set) is skipped rather than producing a bogus path.
func TestDiscoverUnresolvableBase(t *testing.T) {
	d := Descriptor{ID: "x", Roots: []RootSpec{{Base: BaseHome, Rel: ".x", Glob: "*.jsonl"}}}
	env := Env{GOOS: "linux", Home: "", Getenv: func(string) string { return "" }, FS: fstest.MapFS{}}

	sources, stats, err := Discover(context.Background(), d, env, DefaultBudget(time.Now()))
	if err != nil {
		t.Fatalf("Discover: unexpected error: %v", err)
	}
	if len(sources) != 0 || stats.FilesSeen != 0 {
		t.Errorf("sources = %+v, stats = %+v; want nothing seen", sources, stats)
	}
}

// TestDiscoverMalformedGlobSkipsRoot proves a descriptor bug (an
// unparseable glob) skips that root instead of failing the whole scan.
func TestDiscoverMalformedGlobSkipsRoot(t *testing.T) {
	mapFS := fstest.MapFS{
		"home/user/.ok/a.jsonl": {Data: []byte("x"), ModTime: mustParse(t, "2026-01-01T00:00:00Z")},
	}
	d := Descriptor{
		ID: "x",
		Roots: []RootSpec{
			{Base: BaseHome, Rel: ".bad", Glob: "["}, // syntactically invalid
			{Base: BaseHome, Rel: ".ok", Glob: "*.jsonl"},
		},
	}
	env := Env{GOOS: "linux", Home: "home/user", Getenv: func(string) string { return "" }, FS: mapFS}

	sources, _, err := Discover(context.Background(), d, env, DefaultBudget(time.Now()))
	if err != nil {
		t.Fatalf("Discover: unexpected error: %v", err)
	}
	if len(sources) != 1 || sources[0].Path != "/home/user/.ok/a.jsonl" {
		t.Errorf("sources = %+v, want only the well-formed root's file", sources)
	}
}

// TestDiscoverSkipsDirectoryMatches proves a glob that happens to match a
// directory entry (not just files) is filtered out rather than returned as
// a bogus zero-byte Source.
func TestDiscoverSkipsDirectoryMatches(t *testing.T) {
	mapFS := fstest.MapFS{
		"home/user/.x/sub/dummy.jsonl": {Data: []byte("x"), ModTime: mustParse(t, "2026-01-01T00:00:00Z")},
	}
	d := Descriptor{ID: "x", Roots: []RootSpec{{Base: BaseHome, Rel: ".x", Glob: "*"}}}
	env := Env{GOOS: "linux", Home: "home/user", Getenv: func(string) string { return "" }, FS: mapFS}

	sources, stats, err := Discover(context.Background(), d, env, DefaultBudget(time.Now()))
	if err != nil {
		t.Fatalf("Discover: unexpected error: %v", err)
	}
	if len(sources) != 0 {
		t.Errorf("sources = %+v, want none (the only match is the \"sub\" directory)", sources)
	}
	if stats.FilesSeen != 0 {
		t.Errorf("FilesSeen = %d, want 0 (a directory match isn't a file)", stats.FilesSeen)
	}
}

// TestDiscoverTieBreaksByPath proves two Sources with an identical ModTime
// come back in a stable, deterministic order (by Path) instead of
// depending on filesystem iteration order.
func TestDiscoverTieBreaksByPath(t *testing.T) {
	same := mustParse(t, "2026-01-01T00:00:00Z")
	mapFS := fstest.MapFS{
		"home/user/.x/b.jsonl": {Data: []byte("x"), ModTime: same},
		"home/user/.x/a.jsonl": {Data: []byte("x"), ModTime: same},
	}
	d := Descriptor{ID: "x", Roots: []RootSpec{{Base: BaseHome, Rel: ".x", Glob: "*.jsonl"}}}
	env := Env{GOOS: "linux", Home: "home/user", Getenv: func(string) string { return "" }, FS: mapFS}

	sources, _, err := Discover(context.Background(), d, env, DefaultBudget(time.Now()))
	if err != nil {
		t.Fatalf("Discover: unexpected error: %v", err)
	}
	if len(sources) != 2 || sources[0].Path != "/home/user/.x/a.jsonl" || sources[1].Path != "/home/user/.x/b.jsonl" {
		t.Errorf("sources = %+v, want a.jsonl then b.jsonl (tie-broken by path)", sources)
	}
}

// TestDiscoverMaxFilesCutoff proves the MaxFiles cap, not just the
// deadline, sets BudgetHit and drops the oldest files.
func TestDiscoverMaxFilesCutoff(t *testing.T) {
	mapFS := fstest.MapFS{
		"home/user/.x/old.jsonl": {Data: []byte("x"), ModTime: mustParse(t, "2026-01-01T00:00:00Z")},
		"home/user/.x/new.jsonl": {Data: []byte("x"), ModTime: mustParse(t, "2026-01-02T00:00:00Z")},
	}
	d := Descriptor{ID: "x", Roots: []RootSpec{{Base: BaseHome, Rel: ".x", Glob: "*.jsonl"}}}
	env := Env{GOOS: "linux", Home: "home/user", Getenv: func(string) string { return "" }, FS: mapFS}
	budget := Budget{Deadline: time.Now().Add(time.Hour), MaxFiles: 1, MaxBytesFile: 1 << 30, MaxTotalBytes: 1 << 30}

	sources, stats, err := Discover(context.Background(), d, env, budget)
	if err != nil {
		t.Fatalf("Discover: unexpected error: %v", err)
	}
	if !stats.BudgetHit {
		t.Error("BudgetHit = false, want true")
	}
	if len(sources) != 1 || sources[0].Path != "/home/user/.x/new.jsonl" {
		t.Errorf("sources = %+v, want only new.jsonl", sources)
	}
}

// TestDiscoverMaxTotalBytesCutoff proves the total-bytes cap also sets
// BudgetHit and drops the oldest files once the cap would be exceeded.
func TestDiscoverMaxTotalBytesCutoff(t *testing.T) {
	mapFS := fstest.MapFS{
		"home/user/.x/old.jsonl": {Data: []byte("12345"), ModTime: mustParse(t, "2026-01-01T00:00:00Z")},
		"home/user/.x/new.jsonl": {Data: []byte("12345"), ModTime: mustParse(t, "2026-01-02T00:00:00Z")},
	}
	d := Descriptor{ID: "x", Roots: []RootSpec{{Base: BaseHome, Rel: ".x", Glob: "*.jsonl"}}}
	env := Env{GOOS: "linux", Home: "home/user", Getenv: func(string) string { return "" }, FS: mapFS}
	budget := Budget{Deadline: time.Now().Add(time.Hour), MaxFiles: 100, MaxBytesFile: 1 << 30, MaxTotalBytes: 5}

	sources, stats, err := Discover(context.Background(), d, env, budget)
	if err != nil {
		t.Fatalf("Discover: unexpected error: %v", err)
	}
	if !stats.BudgetHit {
		t.Error("BudgetHit = false, want true")
	}
	if len(sources) != 1 || sources[0].Path != "/home/user/.x/new.jsonl" {
		t.Errorf("sources = %+v, want only new.jsonl (5 bytes fits, the second 5 would not)", sources)
	}
}

// TestEnvFSReadOnlyByConstruction asserts Discover can never open a file in
// write mode: fs.File, the only thing fs.FS.Open can return, exposes no
// Write method, so Env.FS is read-only by construction regardless of what
// concrete type backs it.
func TestEnvFSReadOnlyByConstruction(t *testing.T) {
	var _ fs.FS = fstest.MapFS{}
	fileType := reflect.TypeOf((*fs.File)(nil)).Elem()
	if _, ok := fileType.MethodByName("Write"); ok {
		t.Fatal("fs.File exposes a Write method; Env.FS would no longer be read-only by construction")
	}
}

func TestResolveBaseXDGData(t *testing.T) {
	tests := []struct {
		name   string
		getenv func(string) string
		want   string
	}{
		{
			name:   "unset falls back to <home>/.local/share",
			getenv: func(string) string { return "" },
			want:   "home/user/.local/share",
		},
		{
			name: "set wins outright",
			getenv: func(k string) string {
				if k == "XDG_DATA_HOME" {
					return "custom/data"
				}
				return ""
			},
			want: "custom/data",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := Env{GOOS: "linux", Home: "home/user", Getenv: tt.getenv}
			got, ok := resolveBase(RootSpec{Base: BaseXDGData}, env, "")
			if !ok {
				t.Fatalf("resolveBase: ok = false, want true")
			}
			if got != tt.want {
				t.Errorf("resolveBase = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveBaseAppDataWindowsOnly(t *testing.T) {
	windowsEnv := Env{
		GOOS: "windows",
		Getenv: func(k string) string {
			if k == "APPDATA" {
				return `C:\Users\me\AppData\Roaming`
			}
			return ""
		},
	}
	got, ok := resolveBase(RootSpec{Base: BaseAppData}, windowsEnv, "")
	if !ok {
		t.Fatal("resolveBase on windows: ok = false, want true")
	}
	if want := "C:/Users/me/AppData/Roaming"; got != want {
		t.Errorf("resolveBase = %q, want %q", got, want)
	}

	linuxEnv := Env{GOOS: "linux", Getenv: func(string) string { return `C:\should\not\be\used` }}
	if _, ok := resolveBase(RootSpec{Base: BaseAppData}, linuxEnv, ""); ok {
		t.Error("resolveBase on linux for BaseAppData: ok = true, want false (windows-only base)")
	}
}

func TestResolveBaseRemainingCases(t *testing.T) {
	tests := []struct {
		name     string
		spec     RootSpec
		env      Env
		override string
		wantOK   bool
		want     string
	}{
		{
			name:   "BaseHome resolves to Home",
			spec:   RootSpec{Base: BaseHome},
			env:    Env{Home: "home/user", Getenv: func(string) string { return "" }},
			wantOK: true,
			want:   "home/user",
		},
		{
			name:   "BaseHome with no Home set",
			spec:   RootSpec{Base: BaseHome},
			env:    Env{Home: "", Getenv: func(string) string { return "" }},
			wantOK: false,
		},
		{
			name:   "BaseXDGConfig unset falls back to <home>/.config",
			spec:   RootSpec{Base: BaseXDGConfig},
			env:    Env{Home: "home/user", Getenv: func(string) string { return "" }},
			wantOK: true,
			want:   "home/user/.config",
		},
		{
			name: "BaseXDGConfig set wins outright",
			spec: RootSpec{Base: BaseXDGConfig},
			env: Env{Home: "home/user", Getenv: func(k string) string {
				if k == "XDG_CONFIG_HOME" {
					return "custom/config"
				}
				return ""
			}},
			wantOK: true,
			want:   "custom/config",
		},
		{
			name:   "BaseLocalAppData on non-windows never resolves",
			spec:   RootSpec{Base: BaseLocalAppData},
			env:    Env{GOOS: "linux", Getenv: func(string) string { return `C:\unused` }},
			wantOK: false,
		},
		{
			name: "BaseLocalAppData on windows resolves to %LOCALAPPDATA%",
			spec: RootSpec{Base: BaseLocalAppData},
			env: Env{GOOS: "windows", Getenv: func(k string) string {
				if k == "LOCALAPPDATA" {
					return `C:\Users\me\AppData\Local`
				}
				return ""
			}},
			wantOK: true,
			want:   "C:/Users/me/AppData/Local",
		},
		{
			name:   "BaseLocalAppData on windows with unset env",
			spec:   RootSpec{Base: BaseLocalAppData},
			env:    Env{GOOS: "windows", Getenv: func(string) string { return "" }},
			wantOK: false,
		},
		{
			name:   "unknown Base never resolves",
			spec:   RootSpec{Base: "bogus"},
			env:    Env{Home: "home/user", Getenv: func(string) string { return "" }},
			wantOK: false,
		},
		{
			name:     "an override wins over everything",
			spec:     RootSpec{Base: BaseHome},
			env:      Env{Home: "home/user", Getenv: func(string) string { return "" }},
			override: `C:\custom\home`,
			wantOK:   true,
			want:     "C:/custom/home",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := resolveBase(tt.spec, tt.env, tt.override)
			if ok != tt.wantOK {
				t.Fatalf("resolveBase: ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("resolveBase = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestToRealPathWindows(t *testing.T) {
	if got, want := toRealPath("windows", "C:/Users/me/x.jsonl"), `C:\Users\me\x.jsonl`; got != want {
		t.Errorf("toRealPath(windows, ...) = %q, want %q", got, want)
	}
}
