# Metrics contract

This is the source of truth for every metric that `ai-usage-exporter` exposes
at `/metrics`. Any provider or collector that adds or modifies a metric must
update this document in the same PR.

Namespace prefix: `ai_usage_`. **All metrics are gauges** — see
[ADR-001](adr/0001-startup-scan-and-gauges.md) for why usage series never have
a `_total` suffix and are never modeled as counters.

Metric names are a public API: a rename breaks existing dashboards, so it must
be discussed in the corresponding issue before being made.

## Allowed labels

The exporter emits only these four labels on metrics covered by this contract:

| Label | Meaning | Values |
|---|---|---|
| `tool` | AI agent that generated the usage | `claude-code`, `codex`, `opencode`, ... |
| `model` | Model used by that tool | e.g. `claude-opus-4`, `gpt-5-codex` |
| `token_type` | Category of counted tokens | `input`, `output`, `cache_read`, `cache_write`, `reasoning` |
| `window` | Time window aggregated at scan time | `1h`, `24h`, `7d`, `30d`, `mtd`, `all` |

`session_id` is **never** a label, under any circumstance, in any future
metric. It is the classic cardinality trap in exporters of this kind: thousands
of dead series that Prometheus never needs again after the scan that generated
them. If data needs session-level granularity, it belongs in logs (`log/slog`),
never in a label.

`project` is **opt-in and disabled by default** (`labels.project: false` in
the configuration). When enabled, its value is normalized and **truncated to
48 characters**; the full on-disk project path is never used.

### Cardinality budget

Prometheus adds `instance` and `job` at scrape time. They are not exporter
labels and therefore do not expand the label contract above. The declared
cardinality budget is **per instance**; a Prometheus server scraping multiple
exporters has the same budget for each scraped target.

Declared bound for the expected worst case:

```
5 tools × 30 models × 5 token_type × 6 window = 4,500 series
```

This bound is a design rule, not a suggestion: every proposed new label must
calculate its impact on this multiplication before it is accepted. The `1h`
window adds 750 series (+20%) over the original five windows; it is accepted
because it enables monitoring of recent usage without introducing labels or
persistent state.

## Metrics

### `ai_usage_tokens`

- **Type:** gauge
- **Labels:** `tool`, `model`, `token_type`, `window`
- **Unit:** tokens (a count, with no Prometheus base unit)
- **Meaning:** number of `token_type` tokens consumed by `model` within
  `tool`, aggregated over the `window` window according to the local history
  available at scan time. Deduplicated by message ID, never by raw log line.
- **Absent**, never materialized as `0`, when no event produced that
  (`tool`, `model`, `token_type`, `window`) combination. `model` and
  `token_type` form an open set in practice; inventing their empty combinations
  would increase cardinality without a useful bound.

#### Invariant: `token_type` classes are disjoint

The five `token_type` classes partition an event's tokens: every token is
counted **exactly once**, in a single class. Specifically, `input` means
**non-cached input**: it always excludes anything already counted under
`cache_read` and `cache_write`.

This is an exporter invariant, not an invariant of the tools it reads, and each
provider is responsible for preserving it while normalizing its native format:

- Tools whose format already reports input **excluding** cached input (for
  example, Claude Code, whose `input_tokens` is disjoint from
  `cache_read_input_tokens`) map the fields directly.
- Tools whose format reports input **including** cached input (for example,
  Codex, whose `last_input_tokens` includes `last_cached_tokens`; this is the
  OpenAI API convention, where `cached_tokens` is a `prompt_tokens` detail)
  must subtract the cached portion before emitting `input`, flooring at zero.

This is a contract rather than a detail of each provider for two reasons:

1. **Cost.** `ai_usage_cost_usd` sums by class, and cached tokens have their
   own price, typically an order of magnitude lower. Counting the same token
   in `input` and `cache_read` overstates cost, increasingly so as caching
   works better — the opposite of what users expect to see.
2. **Comparability between tools.** Without this invariant,
   `sum by (tool) (ai_usage_tokens)` is not comparable between two distinct
   `tool` values, which is the primary question this exporter exists to answer.

Verifiable corollary for every new provider: for one event, the sum of the five
classes must equal the total tokens the tool itself reports, when it reports
one.

### `ai_usage_cost_usd`

- **Type:** gauge
- **Labels:** `tool`, `model`, `window`
- **Unit:** USD
- **Meaning:** estimated dollar cost attributable to `model` within `tool` in
  the `window` window, derived from consumed tokens and the price table in
  effect at scan time.
- **Absent**, never `0`, when no measured tokens exist for that
  (`tool`, `model`, `window`) combination or when the catalog does not know the
  (`tool`, `model`) pair — a new model is not a free model.

#### Reconciliation with a provider's native cost

Some formats (OpenCode, for example) already contain their own cost field.
**The price catalog always takes precedence over that native cost**: every tool
is compared under the same pricing rule instead of contributing its own notion
of a dollar. In practice, this is not even a decision that the collector has to
arbitrate at runtime: `model.UsageEvent` has no cost field, so
`ai_usage_cost_usd` can only be derived from the catalog — there is no other
source to reconcile.

### `ai_usage_sessions`

- **Type:** gauge
- **Labels:** `tool`, `window`
- **Unit:** sessions (count)
- **Meaning:** number of distinct `tool` sessions with at least one event in
  the `window` window. A session is counted once even if it was resumed
  (`/resume`) or forked.
- For every available provider, one series is materialized for each supported
  window; `0` means a healthy scan found no sessions in that window. No series
  is emitted for an unavailable provider.

### `ai_usage_tool_calls`

- **Type:** gauge
- **Labels:** `tool`, `window`
- **Unit:** calls (count)
- **Meaning:** number of tool invocations (model function/tool calls) recorded
  by `tool` within the `window` window.
- For every available provider that has reported tool calls in any window, one
  series is materialized for each supported window; `0` means a healthy scan
  found no tool calls in that window. No series is emitted for an unavailable
  provider or one whose format does not report tool calls.

## Zero-value contract and live monitoring

Whether zeroes are materialized depends on whether the label set is closed:

1. `ai_usage_sessions` has only `tool` and `window`, which are enumerable
   sets; every available provider publishes all six windows, so missing events
   are represented by `0`. `ai_usage_tool_calls` does the same only for tools
   that have previously reported tool calls: a format that cannot count them
   remains absent because unknown is not zero.
2. `ai_usage_tokens` and `ai_usage_cost_usd` include `model` (and tokens also
   include `token_type`), which are open sets; they are published only when
   measured consumption exists. Models, classes, and costs at zero are not
   invented.
3. The "Now" row panels convert aggregate absence into zero with
   `or vector(0)` and complete the per-tool chart with
   `or (0 * max by (tool) (ai_usage_provider_available))`.
4. Missing data is not a consumption alert: availability or freshness alerts
   must query `ai_usage_provider_available` and
   `ai_usage_scan_timestamp_seconds`, not the presence of token series.

### `ai_usage_last_event_timestamp_seconds`

- **Type:** gauge
- **Labels:** `tool`
- **Unit:** seconds (Unix timestamp UTC)
- **Meaning:** timestamp of the newest event found in `tool`'s local history
  at scan time. It detects inactive tools or data sources that stopped
  receiving events.

### `ai_usage_provider_available`

- **Type:** gauge
- **Labels:** `tool`
- **Unit:** boolean (`0` or `1`)
- **Meaning:** `1` if the `tool` provider could locate and read its data source
  during this scan; `0` if the source does not exist, is not readable, or
  parsing failed completely. It does not distinguish "never installed" from
  "failed to read" — that belongs in logs.

### `ai_usage_scan_timestamp_seconds`

- **Type:** gauge
- **Labels:** none
- **Unit:** seconds (Unix timestamp UTC)
- **Meaning:** time when the process completed the scan whose snapshot it is
  serving at `/metrics` now. By default, the exporter repeats a complete scan
  every 60 seconds; this value changes after every successful rescan. With
  `scan_interval: 0`, it retains the startup scan timestamp until `SIGHUP` or a
  restart.

### `ai_usage_scan_success`

- **Type:** gauge
- **Labels:** none
- **Unit:** boolean (`0` or `1`)
- **Meaning:** `1` when the last scan completed in full and its snapshot is
  safe to publish; `0` when it failed or timed out. An initial failure exposes
  no partial usage gauges. See [ADR-007](adr/0007-degraded-startup-scan.md).

### `ai_usage_scan_duration_seconds`

- **Type:** gauge
- **Labels:** none
- **Unit:** seconds
- **Meaning:** duration of the most recent complete scan of all providers.
  Useful for detecting degradation as local log volume grows.

## Scan cost and cadence

The default `scan_interval` is `60s`; `0` disables it for users who want a
frozen snapshot. In a 2026-10-08 measurement on macOS arm64 with Go 1.27.1 and
`CGO_ENABLED=0`, a warm scan of 40 Claude Code JSONL files, 276 Codex JSONL
files, and a 2.7 GB OpenCode SQLite source took 0.68 s (≈1.1% of a 60-second
interval). To recalculate the budget on another machine with its own data, run
this three times and use the warm runs:

```bash
/usr/bin/time -p ai-usage-exporter doctor --output json
```

### `ai_usage_scan_files`

- **Type:** gauge
- **Labels:** `tool`
- **Unit:** files (count)
- **Meaning:** number of files (or, for SQLite providers, processed
  rows/sessions) that the `tool` provider read in this scan.

### `ai_usage_scan_errors`

- **Type:** gauge
- **Labels:** `tool`
- **Unit:** errors (count)
- **Meaning:** number of files or records that the `tool` provider could not
  parse in this scan (unexpected format, corrupted file, and so on). A value
  greater than zero does not necessarily imply `ai_usage_provider_available=0`
  if the provider could recover and continue with the rest of the source.

### `ai_usage_build_info`

- **Type:** gauge
- **Labels:** `version`, `commit`, `go_version`
- **Unit:** dimensionless, always `1`
- **Meaning:** build metadata for the running binary, following the standard
  Prometheus `*_build_info` convention. The value is always `1`; the
  information is in the labels, not the value.

## Privacy

No metric in this contract, present or future, may expose prompt content,
model output, or tool output. The exporter reads usage metadata only (tokens,
model, timestamps, and counts). This is a product contract, not an
implementation preference — see the privacy invariant in the project
instructions.
