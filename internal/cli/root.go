// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

// Package cli implements the exporter command-line interface.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/aggregate"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/config"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/pricing"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider/all"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/scan"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/version"
)

// Execute is the CLI entry point. Output is injected so callers and tests do
// not need to redirect process-global stdout/stderr.
func Execute(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "serve is handled by the command entry point; Execute requires a subcommand")
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(out, version.String())
		return 0
	case "providers":
		return providers(args[1:], out, errOut)
	case "report":
		return report(args[1:], out, errOut)
	case "doctor":
		return doctor(args[1:], out, errOut)
	case "serve":
		fmt.Fprintln(errOut, "serve is handled by the command entry point")
		return 2 // serve is owned by cmd, where signal lifetime belongs.
	default:
		fmt.Fprintf(errOut, "unknown command %q\n", args[0])
		return 2
	}
}

// registry and environment are seams for deterministic command tests. Execute
// keeps its public contract; production retains the real implementations.
var registry = all.Registry
var environment = provider.OSEnv

func outputFlag(name string, args []string) (string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("output", "table", "table or json")
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if *format != "table" && *format != "json" {
		return "", fmt.Errorf("--output must be table or json")
	}
	return *format, nil
}

type providerView struct {
	ID           string                `json:"id"`
	DisplayName  string                `json:"display_name"`
	Kind         string                `json:"kind"`
	Roots        []provider.RootSpec   `json:"roots"`
	Capabilities provider.Capabilities `json:"capabilities"`
}

func providers(args []string, out, errOut io.Writer) int {
	format, err := outputFlag("providers", args)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	reg, err := registry()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	rows := make([]providerView, 0, len(reg.All()))
	for _, p := range reg.All() {
		d := p.Descriptor()
		rows = append(rows, providerView{d.ID, d.DisplayName, string(d.Kind), d.Roots, d.Capabilities})
	}
	if format == "json" {
		_ = json.NewEncoder(out).Encode(rows)
		return 0
	}
	for _, r := range rows {
		fmt.Fprintf(out, "%s\t%s\t%s\n", r.ID, r.DisplayName, r.Kind)
	}
	return 0
}

type reportRow struct {
	Tool      string   `json:"tool"`
	Model     string   `json:"model"`
	TokenType string   `json:"token_type"`
	Tokens    int64    `json:"tokens"`
	CostUSD   *float64 `json:"cost_usd"`
}
type reportView struct {
	ScannedAt time.Time   `json:"scanned_at"`
	Window    string      `json:"window"`
	Rows      []reportRow `json:"rows"`
}

func report(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	window := fs.String("window", "all", "window")
	format := fs.String("output", "table", "table or json")
	var tools list
	fs.Var(&tools, "tool", "provider ID (repeatable)")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	w := aggregate.Window(*window)
	if !validWindow(w) {
		fmt.Fprintln(errOut, "--window must be one of 1h, 24h, 7d, 30d, mtd, all")
		return 2
	}
	if *format != "table" && *format != "json" {
		fmt.Fprintln(errOut, "--output must be table or json")
		return 2
	}
	cfg, err := config.Load("", os.Getenv, nil)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	reg, err := registry()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if len(tools) > 0 {
		reg, err = filtered(reg, tools)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
	}
	tz, err := cfg.Location()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.ScanTimeout)
	defer cancel()
	cat, err := pricing.Load(context.Background(), cfg.Pricing)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	result, err := scan.Run(ctx, reg, environment(), provider.DefaultBudget(time.Now()), time.Now(), tz)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	writeScanWarnings(result, errOut)
	rows := make([]reportRow, 0)
	for k, n := range result.Snapshot.Tokens {
		if k.Window != w {
			continue
		}
		var cost *float64
		if rates, ok := cat.Lookup(k.Tool, k.Model); ok {
			v := float64(n) * rate(k.Class, rates)
			cost = &v
		}
		rows = append(rows, reportRow{k.Tool, k.Model, string(k.Class), n, cost})
	}
	sort.Slice(rows, func(i, j int) bool {
		return strings.Join([]string{rows[i].Tool, rows[i].Model, rows[i].TokenType}, "/") < strings.Join([]string{rows[j].Tool, rows[j].Model, rows[j].TokenType}, "/")
	})
	v := reportView{result.Snapshot.ScannedAt, string(w), rows}
	if *format == "json" {
		_ = json.NewEncoder(out).Encode(v)
	} else {
		for _, r := range rows {
			fmt.Fprintf(out, "%s\t%s\t%s\t%d\t", r.Tool, r.Model, r.TokenType, r.Tokens)
			if r.CostUSD == nil {
				fmt.Fprintln(out, "unknown")
			} else {
				fmt.Fprintf(out, "%.6f\n", *r.CostUSD)
			}
		}
	}
	return 0
}
func validWindow(w aggregate.Window) bool {
	return w == aggregate.Window1h || w == aggregate.Window24h || w == aggregate.Window7d || w == aggregate.Window30d || w == aggregate.WindowMTD || w == aggregate.WindowAll
}
func rate(c model.TokenClass, r pricing.Rates) float64 {
	switch c {
	case model.TokenInput:
		return r.Input
	case model.TokenOutput:
		return r.Output
	case model.TokenCacheRead:
		return r.CacheRead
	case model.TokenCacheWrite:
		return r.CacheWrite
	case model.TokenReasoning:
		return r.Reasoning
	}
	return 0
}

type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(v string) error { *l = append(*l, v); return nil }
func filtered(reg *provider.Registry, ids []string) (*provider.Registry, error) {
	out := provider.NewRegistry()
	for _, id := range ids {
		p, ok := reg.Get(id)
		if !ok {
			return nil, fmt.Errorf("unknown provider %q", id)
		}
		if err := out.Register(p); err != nil {
			return nil, err
		}
	}
	return out, nil
}

type doctorRow struct {
	ID               string   `json:"id"`
	Available        bool     `json:"available"`
	Files            int      `json:"files"`
	Skipped          int      `json:"skipped"`
	SkippedBySize    int      `json:"skipped_by_size"`
	SkippedByType    int      `json:"skipped_by_type"`
	SkippedByBudget  int      `json:"skipped_by_budget"`
	Errors           int      `json:"errors"`
	FirstParseError  string   `json:"first_parse_error,omitempty"`
	BudgetHit        bool     `json:"budget_hit"`
	Roots            []string `json:"roots"`
	Hint             string   `json:"hint"`
	PricingSource    string   `json:"pricing_source"`
	ZeroPricedModels []string `json:"zero_priced_models"`
	UnpricedModels   []string `json:"unpriced_models"`
}

func doctor(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("output", "table", "table or json")
	tool := fs.String("tool", "", "provider ID")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	if *format != "table" && *format != "json" {
		fmt.Fprintln(errOut, "--output must be table or json")
		return 2
	}
	cfg, err := config.Load("", os.Getenv, nil)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	reg, err := registry()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if *tool != "" {
		reg, err = filtered(reg, []string{*tool})
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
	}
	tz, err := cfg.Location()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.ScanTimeout)
	defer cancel()
	cat, err := pricing.Load(context.Background(), cfg.Pricing)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	result, err := scan.Run(ctx, reg, environment(), provider.DefaultBudget(time.Now()), time.Now(), tz)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	writeScanWarnings(result, errOut)
	models := map[string]map[string]bool{}
	for k := range result.Snapshot.Tokens {
		if models[k.Tool] == nil {
			models[k.Tool] = map[string]bool{}
		}
		models[k.Tool][k.Model] = true
	}
	rows := make([]doctorRow, 0, len(reg.All()))
	for _, p := range reg.All() {
		d := p.Descriptor()
		tr := result.PerTool[d.ID]
		roots := tr.ResolvedRoots
		if roots == nil {
			roots = []string{}
		}
		row := doctorRow{ID: d.ID, Available: tr.Available, Files: tr.FilesScanned, Skipped: tr.FilesSkipped, SkippedBySize: tr.FilesSkippedBySize, SkippedByType: tr.FilesSkippedByType, SkippedByBudget: tr.FilesSkippedByBudget, Errors: tr.ParseErrors, FirstParseError: tr.FirstParseError, BudgetHit: tr.BudgetHit, Roots: roots, Hint: fmt.Sprintf("roots: %s", strings.Join(roots, ", ")), PricingSource: cat.Source(), ZeroPricedModels: []string{}, UnpricedModels: []string{}}
		for m := range models[d.ID] {
			r, ok := cat.Lookup(d.ID, m)
			if !ok {
				row.UnpricedModels = append(row.UnpricedModels, m)
			} else if r.Input == 0 && r.Output == 0 && r.CacheRead == 0 && r.CacheWrite == 0 && r.Reasoning == 0 {
				row.ZeroPricedModels = append(row.ZeroPricedModels, m)
			}
		}
		sort.Strings(row.UnpricedModels)
		sort.Strings(row.ZeroPricedModels)
		rows = append(rows, row)
	}
	if *format == "json" {
		_ = json.NewEncoder(out).Encode(rows)
	} else {
		for _, r := range rows {
			fmt.Fprintf(out, "%s\tavailable=%t\tfiles=%d\tskipped=%d (size=%d type=%d budget=%d)\terrors=%d\tbudget_hit=%t", r.ID, r.Available, r.Files, r.Skipped, r.SkippedBySize, r.SkippedByType, r.SkippedByBudget, r.Errors, r.BudgetHit)
			if r.FirstParseError != "" {
				fmt.Fprintf(out, "\tfirst_parse_error=%s", r.FirstParseError)
			}
			fmt.Fprintln(out)
		}
	}
	return 0
}

// writeScanWarnings keeps structured stdout safe for pipes while still
// telling an interactive caller that one provider could not parse every
// source. It deliberately reports counts only: provider errors must never
// leak prompt or tool-output content through the CLI.
func writeScanWarnings(result scan.Result, errOut io.Writer) {
	ids := make([]string, 0, len(result.PerTool))
	for id := range result.PerTool {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if count := result.PerTool[id].ParseErrors; count > 0 {
			fmt.Fprintf(errOut, "warning: %s: %d parse errors\n", id, count)
		}
	}
}
