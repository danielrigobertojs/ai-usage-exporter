// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

// Package config resolves the exporter's runtime configuration from, in
// ascending precedence, built-in defaults, an optional YAML file, AI_USAGE_*
// environment variables, and command-line flags.
package config

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/pricing"
)

// Config is the exporter's fully resolved runtime configuration.
type Config struct {
	Listen       string        `yaml:"listen"`
	MetricsPath  string        `yaml:"metrics_path"`
	ScanInterval time.Duration `yaml:"scan_interval"`
	ScanTimeout  time.Duration `yaml:"scan_timeout"`
	Timezone     string        `yaml:"timezone"`
	Providers    []string      `yaml:"providers"`
	LogLevel     string        `yaml:"log_level"`
	LogFormat    string        `yaml:"log_format"`
	Labels       struct {
		Project bool `yaml:"project"`
	} `yaml:"labels"`
	Pricing pricing.Config `yaml:"pricing"`
}

func defaultConfig() Config {
	return Config{
		Listen:       "127.0.0.1:9477",
		MetricsPath:  "/metrics",
		ScanInterval: time.Minute,
		ScanTimeout:  30 * time.Second,
		Timezone:     "Local",
		LogLevel:     "info",
		LogFormat:    "text",
	}
}

// DefaultPath returns the platform-conventional config file location:
// <xdg_config>/ai-usage-exporter/config.yaml on darwin/linux (honoring
// XDG_CONFIG_HOME), %APPDATA%\ai-usage-exporter\config.yaml on windows. It
// returns "" when the underlying directory can't be resolved, in which case
// Load simply treats the config file as absent rather than erroring.
func DefaultPath() string {
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "ai-usage-exporter", "config.yaml")
		}
		return ""
	}

	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "ai-usage-exporter", "config.yaml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "ai-usage-exporter", "config.yaml")
}

// Load resolves a Config from, in ascending precedence: defaults, the YAML
// file at path (DefaultPath() if path is ""), AI_USAGE_* variables read
// through env, and flags parsed from args. A missing config file is not an
// error - it is the common case for a user who never created one.
func Load(path string, env func(string) string, args []string) (Config, error) {
	cfg := defaultConfig()

	if path == "" {
		path = DefaultPath()
	}
	if path != "" {
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				return Config{}, fmt.Errorf("config: parse %s: %w", path, err)
			}
		case errors.Is(err, os.ErrNotExist):
			// No config file is the common case; defaults stand.
		default:
			return Config{}, fmt.Errorf("config: read %s: %w", path, err)
		}
	}

	if env != nil {
		if err := applyEnv(&cfg, env); err != nil {
			return Config{}, err
		}
	}

	if err := applyFlags(&cfg, args); err != nil {
		return Config{}, err
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// applyEnv overlays AI_USAGE_* variables onto cfg. An unset or empty
// variable leaves the corresponding field untouched.
func applyEnv(cfg *Config, env func(string) string) error {
	if v := env("AI_USAGE_LISTEN"); v != "" {
		cfg.Listen = v
	}
	if v := env("AI_USAGE_METRICS_PATH"); v != "" {
		cfg.MetricsPath = v
	}
	if v := env("AI_USAGE_SCAN_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("config: AI_USAGE_SCAN_INTERVAL: %w", err)
		}
		cfg.ScanInterval = d
	}
	if v := env("AI_USAGE_SCAN_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("config: AI_USAGE_SCAN_TIMEOUT: %w", err)
		}
		cfg.ScanTimeout = d
	}
	if v := env("AI_USAGE_TIMEZONE"); v != "" {
		cfg.Timezone = v
	}
	if v := env("AI_USAGE_PROVIDERS"); v != "" {
		cfg.Providers = splitList(v)
	}
	if v := env("AI_USAGE_LOG_LEVEL"); v != "" {
		cfg.LogLevel = strings.ToLower(v)
	}
	if v := env("AI_USAGE_LOG_FORMAT"); v != "" {
		cfg.LogFormat = strings.ToLower(v)
	}
	if v := env("AI_USAGE_LABELS_PROJECT"); v != "" {
		b, err := parseBool(v)
		if err != nil {
			return fmt.Errorf("config: AI_USAGE_LABELS_PROJECT: %w", err)
		}
		cfg.Labels.Project = b
	}
	return nil
}

// applyFlags overlays flags parsed from args onto cfg. Every flag defaults
// to the empty string so Load can tell "not passed" apart from "passed
// explicitly", matching applyEnv's precedence contract: a flag only
// overrides what the file and environment already resolved when the user
// actually supplied it.
func applyFlags(cfg *Config, args []string) error {
	fs := flag.NewFlagSet("ai-usage-exporter", flag.ContinueOnError)

	var listen, metricsPath, scanInterval, scanTimeout, timezone, providers, labelsProject, logLevel, logFormat string
	fs.StringVar(&listen, "listen", "", "address to listen on, e.g. 127.0.0.1:9477")
	fs.StringVar(&metricsPath, "metrics-path", "", "HTTP path to serve /metrics on")
	fs.StringVar(&scanInterval, "scan-interval", "", "re-scan interval, e.g. 5m (0 disables re-scanning)")
	fs.StringVar(&scanTimeout, "scan-timeout", "", "per-scan timeout, e.g. 30s")
	fs.StringVar(&timezone, "timezone", "", "IANA timezone (or \"Local\") month-to-date boundaries are computed in")
	fs.StringVar(&providers, "providers", "", "comma-separated provider IDs to scan (empty means every registered provider)")
	fs.StringVar(&labelsProject, "labels-project", "", "true/false: opt into the project label (see docs/metrics.md)")
	fs.StringVar(&logLevel, "log-level", "", "debug, info, warn, or error")
	fs.StringVar(&logFormat, "log-format", "", "text or json")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("config: parse flags: %w", err)
	}

	if listen != "" {
		cfg.Listen = listen
	}
	if metricsPath != "" {
		cfg.MetricsPath = metricsPath
	}
	if scanInterval != "" {
		d, err := time.ParseDuration(scanInterval)
		if err != nil {
			return fmt.Errorf("config: --scan-interval: %w", err)
		}
		cfg.ScanInterval = d
	}
	if scanTimeout != "" {
		d, err := time.ParseDuration(scanTimeout)
		if err != nil {
			return fmt.Errorf("config: --scan-timeout: %w", err)
		}
		cfg.ScanTimeout = d
	}
	if timezone != "" {
		cfg.Timezone = timezone
	}
	if providers != "" {
		cfg.Providers = splitList(providers)
	}
	if labelsProject != "" {
		b, err := parseBool(labelsProject)
		if err != nil {
			return fmt.Errorf("config: --labels-project: %w", err)
		}
		cfg.Labels.Project = b
	}
	if logLevel != "" {
		cfg.LogLevel = strings.ToLower(logLevel)
	}
	if logFormat != "" {
		cfg.LogFormat = strings.ToLower(logFormat)
	}
	return nil
}

// validate reports a field-naming error for any value Load cannot act on:
// a negative scan_interval, a non-positive scan_timeout, or a timezone
// Location can't resolve.
func (c Config) validate() error {
	if c.ScanInterval < 0 {
		return fmt.Errorf("config: scan_interval must not be negative, got %s", c.ScanInterval)
	}
	if c.ScanTimeout <= 0 {
		return fmt.Errorf("config: scan_timeout must be positive, got %s", c.ScanTimeout)
	}
	if _, err := c.Location(); err != nil {
		return fmt.Errorf("config: timezone: %w", err)
	}
	if !validLogLevel(c.LogLevel) {
		return fmt.Errorf("config: log_level must be debug, info, warn, or error, got %q", c.LogLevel)
	}
	if c.LogFormat != "text" && c.LogFormat != "json" {
		return fmt.Errorf("config: log_format must be text or json, got %q", c.LogFormat)
	}
	return nil
}

func validLogLevel(v string) bool { return v == "debug" || v == "info" || v == "warn" || v == "error" }

// Location resolves c.Timezone to a *time.Location, treating "" and
// "Local" the same way: the host's local timezone.
func (c Config) Location() (*time.Location, error) {
	if c.Timezone == "" || c.Timezone == "Local" {
		return time.Local, nil
	}
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return nil, fmt.Errorf("unknown timezone %q: %w", c.Timezone, err)
	}
	return loc, nil
}

func splitList(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseBool(v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "t", "true", "yes", "on":
		return true, nil
	case "0", "f", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean %q", v)
	}
}
