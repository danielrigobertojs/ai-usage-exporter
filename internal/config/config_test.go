// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func noEnv(string) string { return "" }

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// TestLoadDefaultsWithoutFile covers step 1: with an absent config file and
// no env or flags, Load returns exactly the built-in defaults.
func TestLoadDefaultsWithoutFile(t *testing.T) {
	cfg, err := Load("testdata/does-not-exist.yaml", noEnv, nil)
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	want := defaultConfig()
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("Load() = %+v, want defaults %+v", cfg, want)
	}
}

func TestDefaultScanIntervalIsOneMinute(t *testing.T) {
	if got, want := defaultConfig().ScanInterval, time.Minute; got != want {
		t.Errorf("default ScanInterval = %s, want %s", got, want)
	}
}

// TestLoadFileOverridesListenAndScanInterval covers step 1: testdata's
// config.yaml overrides listen and scan_interval but leaves every other
// field at its default.
func TestLoadFileOverridesListenAndScanInterval(t *testing.T) {
	cfg, err := Load("testdata/config.yaml", noEnv, nil)
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if cfg.Listen != "0.0.0.0:19999" {
		t.Errorf("Listen = %q, want %q", cfg.Listen, "0.0.0.0:19999")
	}
	if cfg.ScanInterval != 5*time.Minute {
		t.Errorf("ScanInterval = %s, want %s", cfg.ScanInterval, 5*time.Minute)
	}
	if cfg.MetricsPath != defaultConfig().MetricsPath {
		t.Errorf("MetricsPath = %q, want untouched default %q", cfg.MetricsPath, defaultConfig().MetricsPath)
	}
}

// TestLoadEnvOverridesFile covers step 1: AI_USAGE_LISTEN wins over the
// file's listen value.
func TestLoadEnvOverridesFile(t *testing.T) {
	env := envMap(map[string]string{"AI_USAGE_LISTEN": "10.0.0.1:1234"})
	cfg, err := Load("testdata/config.yaml", env, nil)
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if cfg.Listen != "10.0.0.1:1234" {
		t.Errorf("Listen = %q, want %q (env must win over file)", cfg.Listen, "10.0.0.1:1234")
	}
	// The file's scan_interval still applies: env only overrides what it sets.
	if cfg.ScanInterval != 5*time.Minute {
		t.Errorf("ScanInterval = %s, want %s (untouched by env)", cfg.ScanInterval, 5*time.Minute)
	}
}

// TestLoadFlagOverridesEnv covers step 1: --listen wins over
// AI_USAGE_LISTEN.
func TestLoadFlagOverridesEnv(t *testing.T) {
	env := envMap(map[string]string{"AI_USAGE_LISTEN": "10.0.0.1:1234"})
	cfg, err := Load("testdata/config.yaml", env, []string{"--listen", "127.0.0.1:1"})
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if cfg.Listen != "127.0.0.1:1" {
		t.Errorf("Listen = %q, want %q (flag must win over env)", cfg.Listen, "127.0.0.1:1")
	}
}

// TestLoadRejectsNegativeScanInterval covers step 2: a negative
// scan_interval is rejected with an error naming the field.
func TestLoadRejectsNegativeScanInterval(t *testing.T) {
	_, err := Load("", noEnv, []string{"--scan-interval", "-1s"})
	if err == nil {
		t.Fatal("Load: want error for negative scan_interval, got nil")
	}
	if !strings.Contains(err.Error(), "scan_interval") {
		t.Errorf("error %q does not name the scan_interval field", err)
	}
}

// TestLoadRejectsUnknownTimezone covers step 2: a timezone LoadLocation
// can't resolve is rejected with an error naming the field.
func TestLoadRejectsUnknownTimezone(t *testing.T) {
	_, err := Load("", noEnv, []string{"--timezone", "Not/A_Real_Zone"})
	if err == nil {
		t.Fatal("Load: want error for unknown timezone, got nil")
	}
	if !strings.Contains(err.Error(), "timezone") {
		t.Errorf("error %q does not name the timezone field", err)
	}
}

func TestLoadRejectsMalformedFile(t *testing.T) {
	_, err := Load("testdata/malformed.yaml", noEnv, nil)
	if err == nil {
		t.Fatal("Load: want error for malformed YAML, got nil")
	}
}

func TestLocationDefaultsToLocal(t *testing.T) {
	cfg := defaultConfig()
	loc, err := cfg.Location()
	if err != nil {
		t.Fatalf("Location: unexpected error: %v", err)
	}
	if loc != time.Local {
		t.Errorf("Location() = %v, want time.Local", loc)
	}
}

func TestProvidersSplitFromEnvAndFlag(t *testing.T) {
	env := envMap(map[string]string{"AI_USAGE_PROVIDERS": "claude-code, codex"})
	cfg, err := Load("", env, nil)
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	want := []string{"claude-code", "codex"}
	if len(cfg.Providers) != len(want) || cfg.Providers[0] != want[0] || cfg.Providers[1] != want[1] {
		t.Errorf("Providers = %v, want %v", cfg.Providers, want)
	}
}
