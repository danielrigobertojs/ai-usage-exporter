// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package provider

import (
	"io/fs"
	"os"
	"runtime"
	"strings"
)

// Env isolates every bit of system access Discover needs, so tests can
// inject a fake clock-free home, env, and filesystem instead of touching the
// real machine. FS is read-only by construction (fs.FS exposes no write
// method) and its root corresponds to the OS root "/": every path handed to
// FS.Open is relative to that root, with no leading slash, matching the
// io/fs contract.
type Env struct {
	GOOS   string
	Home   string
	Getenv func(string) string
	FS     fs.FS
}

// OSEnv returns the Env wired to the real host OS and filesystem.
func OSEnv() Env {
	home, _ := os.UserHomeDir()
	return Env{
		GOOS:   runtime.GOOS,
		Home:   toVirtualPath(home),
		Getenv: os.Getenv,
		FS:     realFS{goos: runtime.GOOS},
	}
}

// realFS maps the virtual, always-slash-separated paths Discover works with
// back onto real OS paths. It exists because Windows has one root per
// drive, not a single "/": goos decides whether a path is reassembled as
// "/foo/bar" (unix) or "foo/bar" taken as-is, already carrying its drive
// letter (e.g. "C:/Users/me", produced by toVirtualPath).
type realFS struct {
	goos string
}

func (r realFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	return os.Open(toRealPath(r.goos, name))
}

// toVirtualPath converts a real, OS-native absolute path into the
// slash-separated, leading-slash-free form used throughout this package:
// deterministic regardless of which OS the calling test happens to run on,
// which matters because Env.GOOS can legitimately differ from the host OS
// in a cross-platform test case.
func toVirtualPath(real string) string {
	return strings.TrimPrefix(strings.ReplaceAll(real, `\`, "/"), "/")
}

// toRealPath reverses toVirtualPath for the given logical goos.
func toRealPath(goos, virtual string) string {
	if goos == "windows" {
		return strings.ReplaceAll(virtual, "/", `\`)
	}
	return "/" + virtual
}

// resolveHomeOverride returns the first non-empty value among d's HomeEnv
// variables, or "" if none is set. A set override relocates every root of
// that descriptor, replacing the usual Base resolution outright - mirroring
// tools like Codex, where CODEX_HOME takes over the tool's whole home
// instead of sitting alongside the user's XDG directories.
func resolveHomeOverride(d Descriptor, env Env) string {
	for _, name := range d.HomeEnv {
		if v := env.Getenv(name); v != "" {
			return v
		}
	}
	return ""
}

// resolveBase returns the virtual base directory a RootSpec resolves to,
// and whether it resolved at all (a tool that's simply not installed, or a
// Base that doesn't apply to env.GOOS, is not an error - see Discover).
func resolveBase(spec RootSpec, env Env, override string) (string, bool) {
	if override != "" {
		return toVirtualPath(override), true
	}
	switch spec.Base {
	case BaseHome:
		if env.Home == "" {
			return "", false
		}
		return toVirtualPath(env.Home), true
	case BaseXDGData:
		if v := env.Getenv("XDG_DATA_HOME"); v != "" {
			return toVirtualPath(v), true
		}
		if env.Home == "" {
			return "", false
		}
		return toVirtualPath(env.Home) + "/.local/share", true
	case BaseXDGConfig:
		if v := env.Getenv("XDG_CONFIG_HOME"); v != "" {
			return toVirtualPath(v), true
		}
		if env.Home == "" {
			return "", false
		}
		return toVirtualPath(env.Home) + "/.config", true
	case BaseAppData:
		if env.GOOS != "windows" {
			return "", false
		}
		if v := env.Getenv("APPDATA"); v != "" {
			return toVirtualPath(v), true
		}
		return "", false
	case BaseLocalAppData:
		if env.GOOS != "windows" {
			return "", false
		}
		if v := env.Getenv("LOCALAPPDATA"); v != "" {
			return toVirtualPath(v), true
		}
		return "", false
	default:
		return "", false
	}
}
