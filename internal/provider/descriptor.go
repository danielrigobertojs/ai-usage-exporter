// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

// Package provider ports CodexBar's ProviderDescriptor/ProviderCatalog
// pattern: a Descriptor is a static, data-only declaration of where a tool
// keeps its logs, and a Provider turns the Sources that Discover finds into
// model.UsageEvent values. Adding a tool means adding a Descriptor and a
// Parse implementation, never touching the scan or aggregation logic.
package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
)

// SourceKind identifies the on-disk format a Descriptor's roots are made of.
type SourceKind string

const (
	SourceJSONL  SourceKind = "jsonl"
	SourceJSON   SourceKind = "json"
	SourceSQLite SourceKind = "sqlite"
)

// BaseDir names a well-known directory that a RootSpec is relative to. It
// stays a closed set of OS-provided locations on purpose: Discover only ever
// expands these, never a user-supplied or scanned-for directory.
type BaseDir string

const (
	BaseHome         BaseDir = "home"         // $HOME / %USERPROFILE%
	BaseXDGData      BaseDir = "xdg_data"     // $XDG_DATA_HOME, default ~/.local/share
	BaseXDGConfig    BaseDir = "xdg_config"   // $XDG_CONFIG_HOME, default ~/.config
	BaseAppData      BaseDir = "appdata"      // %APPDATA%      (windows only)
	BaseLocalAppData BaseDir = "localappdata" // %LOCALAPPDATA% (windows only)
)

// RootSpec is one known location a Descriptor's logs can live under. Scan
// never walks the filesystem: it only ever expands these specs, the same
// discipline CodexBar documents for its own provider sessions.
type RootSpec struct {
	GOOS string  `json:"goos"` // "darwin"|"linux"|"windows"; "" applies to all
	Base BaseDir `json:"base"`
	Rel  string  `json:"rel"`  // e.g. ".claude/projects"
	Glob string  `json:"glob"` // e.g. "*/*.jsonl"; evaluated with doublestar, never filepath.Glob
}

// Capabilities declares what a tool's log format can report, so collectors
// and docs can tell "zero usage" apart from "this field isn't available".
type Capabilities struct {
	HasTokens      bool `json:"has_tokens"`
	HasCacheTokens bool `json:"has_cache_tokens"`
	HasToolCalls   bool `json:"has_tool_calls"`
	HasNativeCost  bool `json:"has_native_cost"` // the format carries USD directly, not just tokens
}

// Descriptor is a tool's static, purely data-driven self-declaration. It
// carries no behavior; Provider.Parse is what turns its Sources into events.
type Descriptor struct {
	ID           string // the `tool` label value; kebab-case, stable forever
	DisplayName  string
	Kind         SourceKind
	HomeEnv      []string // vars that relocate this tool's home, e.g. {"CODEX_HOME"}
	Roots        []RootSpec
	Capabilities Capabilities
}

var descriptorID = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Validate reports whether d is well-formed enough to register and scan:
// ID must be non-empty kebab-case, and every root needs a non-empty glob.
func (d Descriptor) Validate() error {
	if d.ID == "" {
		return fmt.Errorf("provider: descriptor ID is empty")
	}
	if !descriptorID.MatchString(d.ID) {
		return fmt.Errorf("provider: descriptor ID %q is not kebab-case", d.ID)
	}
	if len(d.Roots) == 0 {
		return fmt.Errorf("provider: descriptor %q has no roots", d.ID)
	}
	for i, r := range d.Roots {
		if r.Glob == "" {
			return fmt.Errorf("provider: descriptor %q root %d has an empty glob", d.ID, i)
		}
	}
	return nil
}

// Provider converts a Descriptor's on-disk Sources into UsageEvents.
type Provider interface {
	Descriptor() Descriptor
	// Parse reads a SINGLE Source in streaming fashion and emits events via
	// emit. It must never load the whole file into memory and must never
	// emit message content - metadata only, per the project's privacy
	// invariant.
	Parse(ctx context.Context, src Source, emit func(model.UsageEvent) error) error
}
