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

Diagnostic metrics carry their own closed label sets, which do not extend the
contract above and must never appear on a usage series: `version`, `commit`,
and `go_version` on `ai_usage_build_info`, and `reason` on
`ai_usage_provider_stale`. A diagnostic label is admissible only when its value
set is closed and owned by this exporter — enumerable from our own source, not
from the data a provider happens to contain.

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

Diagnostic series are counted against the same bound. `ai_usage_provider_stale`
materializes its closed `reason` axis for every available provider. Note that
the `5 tools` above is declared headroom, not a count: three providers are
registered today (`claude-code`, `codex`, `opencode`, pinned by
`TestRegistry`), so both numbers are stated on their own basis:

```
measured today:     3 tools x 3 reasons =  9 series   (+0.20% of the 4,500 bound)
declared worst case: 5 tools x 3 reasons = 15 series   (+0.33%)
```

It is accepted because the reason set is closed in our own source, it is the
only query-side signal that a provider read its source and parsed nothing, and
it adds no label to any usage series. See
[ADR-010](adr/0010-provider-liveness-metric.md).

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

#### Schema migrations and `window="all"`

Some providers retain an earlier schema generation beside the current one.
For a message identity found in more than one generation, the newest
generation is authoritative: generations are **not** combined. In particular,
`window="all"` represents the history of the selected generation available at
scan time, not a union of every historical table left by a migration. Its value
for one provider can therefore decrease after an upstream migration if the
current generation contains less history; that is not, by itself, a metrics
regression. See [ADR-009](adr/0009-opencode-schema-generation-authority.md).

A generation change that truncates the history **to zero** is reported by
`ai_usage_provider_stale{reason="no_usage_events"}`. A truncation to a smaller
non-zero total is indistinguishable from genuinely reduced usage, by design:
both are a correct reading of the authoritative generation. Correlate the drop
with `ai_usage_build_info` to tell a deployment apart from a change in
behavior.

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
3. Dashboard panels may zero-fill only closed scopes (`instance`, `tool`, or
   `window`) with `or (0 * max by (<labels>)
   (ai_usage_provider_available{...}))`; the `max by` labels must exactly
   match the left-hand `sum by` labels.
4. `or vector(0)` is valid only for a scalar aggregate without `by (...)`.
5. Dashboard panels grouped by open scopes (`model` or `token_type`) do not
   zero-fill. Their empty state means no measured consumption and must be
   stated in the title and description, with a scalar total in the same row
   that renders `0` for an empty window.
6. The shipped dashboards default `window` to `30d`: narrower nested windows
   empty first and must not be the initial analytical view.
7. Missing data is not a consumption alert: availability or freshness alerts
   must query `ai_usage_provider_available`,
   `ai_usage_provider_stale`, and `ai_usage_scan_timestamp_seconds`, not the
   presence of token series. `ai_usage_provider_stale` is not emitted until
   JCB-358 ships it; until then an alert naming it returns no data, so pair it
   with `ai_usage_provider_available` rather than relying on it alone.
8. A zero-filled usage panel asserts "no measured consumption", which is not
   the same fact as "we read the source and parsed nothing". Zero-fill stays
   (rule 3), but such a panel must never be the only readable element in its
   row: the health row carries `ai_usage_provider_stale` next to
   `ai_usage_provider_available` so the two empties are distinguishable
   on screen. The shipped dashboards gain that element with JCB-358, together
   with the metric. See [ADR-010](adr/0010-provider-liveness-metric.md).

### `ai_usage_last_event_timestamp_seconds`

- **Type:** gauge
- **Labels:** `tool`
- **Unit:** seconds (Unix timestamp UTC)
- **Meaning:** timestamp of the newest event found in `tool`'s local history
  at scan time. It detects inactive tools or data sources that stopped
  receiving events.
- **Absent**, never `0`, for a provider with no parsed events at all: a `0`
  here would advertise 1970 and poison `time() - x` and any `min`/`max` over
  it. Absence together with `ai_usage_provider_available=1` is exactly the
  condition `ai_usage_provider_stale{reason="no_usage_events"}` reports, which
  is the series freshness alerts must use — a panel computing
  `time() - ai_usage_last_event_timestamp_seconds` renders the same "No data"
  for a provider parsing nothing as for an unreachable exporter.

### `ai_usage_provider_available`

- **Type:** gauge
- **Labels:** `tool`
- **Unit:** boolean (`0` or `1`)
- **Meaning:** `1` if the `tool` provider could locate and read its data source
  during this scan; `0` if the source does not exist, is not readable, or
  parsing failed completely. It does not distinguish "never installed" from
  "failed to read" — that belongs in logs.
- **This is a property of the read, not a liveness signal.** `1` says nothing
  about whether the read produced any event: a provider whose source parses
  cleanly and yields zero events reports `1`. Liveness is
  `ai_usage_provider_stale`, and the two together form the provider's full
  health state — see [ADR-010](adr/0010-provider-liveness-metric.md).

### `ai_usage_provider_stale`

> **Status: specified, not yet emitted.** No code emits this metric yet; the
> collector change is JCB-358. Until that lands the series does not exist at
> `/metrics`, so an alert or panel written against it returns no data — which
> is the silent failure this contract exists to close. Treat every rule below
> that names it as taking effect with JCB-358, not with this document.

- **Type:** gauge
- **Labels:** `tool`, `reason`
- **Unit:** boolean (`0` or `1`)
- **Meaning:** `1` when `tool`'s source was read but its usage is not current,
  for the given `reason`; `0` otherwise. The three reasons are mutually
  exclusive, so at most one series per `tool` is `1` in a scan.
- **`reason` values:**

  | Value | Condition | What it means operationally |
  |---|---|---|
  | `no_usage_events` | source read, zero events parsed | We are reading and counting nothing. Act now: a parser or schema regression looks exactly like this. |
  | `source_newer_than_last_event` | source changed >= 7 days after the newest parsed event | The source keeps growing while the parsed stream does not. |
  | `last_event_too_old` | newest parsed event >= 21 days old | The tool is most likely just idle. |

- All three series are materialized for every **available** provider, so all
  three at `0` is a positive assertion of health. No series is emitted for an
  unavailable provider, matching `ai_usage_sessions`.
- Health state machine, read as a pair:

  | `..._available` | `..._stale` | Meaning |
  |---|---|---|
  | `0` | absent | Source missing, unreadable, or every parse failed |
  | `1` | all `0` | Source read, usage current |
  | `1` | `no_usage_events=1` | Source read, nothing counted |
  | `1` | `source_newer_than_last_event=1` | Source growing, parsed stream behind it |
  | `1` | `last_event_too_old=1` | Source current, tool idle |

- Fleet alert for the case that no other series can express, with no
  dependence on series absence:

  ```promql
  max by (instance, tool) (ai_usage_provider_stale{reason="no_usage_events"}) == 1
  ```

- Adding or removing a `reason` value changes this contract: it requires
  updating this document and ADR-010 in the same PR as the code change.

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
