# ADR-010: Liveness is its own metric, separate from availability

- Date: 2026-10-15
- Status: Accepted
- Fix: when a provider's source reads cleanly and still yields no current
  usage, or when a provider changes schema generation

## Context

`ai_usage_provider_available` is documented as a property of the **read**: `1`
if the source was found and read in this scan, `0` if it is missing,
unreadable, or failed to parse completely. It says nothing about whether the
read produced events. Nothing else in the metrics contract does either.

Measured on the fleet host `macmini` on 2026-10-15, 45 minutes after `main`
was deployed by hand:

```
ai_usage_provider_available{tool="opencode"} 1
ai_usage_scan_success                        1
ai_usage_scan_errors{tool="opencode"}        0
up{job="ai-worker",instance="macmini"}       1
ai_usage_tokens{tool="opencode",...}         <absent>
```

The `window="all"` series for OpenCode went from 188,772,982 tokens to nothing.
Its last data point and the last scrape of the previous binary
(`ai_usage_build_info{commit="983c732"}`) share the same second; the next
scrape is `9a27a44`. Every health signal the contract defines stayed green
across that transition.

Three properties of the code make this invisible rather than merely
undiagnosed:

1. `internal/scan/scan.go` already computes the signal. `staleReason` returns
   `no_usage_events`, `source_newer_than_last_event`, or `last_event_too_old`,
   and `markStaleProviders` records it on `ToolResult.StaleReason` and logs it
   at `WARN`. It is read by `doctor` (`stale`, `stale_reason`) and by nothing
   else. JCB-344 scoped it that way deliberately and delivered what it
   promised; the gap is that fleet mode has no `doctor` channel.
2. `ai_usage_last_event_timestamp_seconds{tool}` is emitted by iterating
   `snap.LastEventAt`, so a provider with zero events produces **no series at
   all**. The freshness panel in `ai-usage-live.json`
   (`time() - ai_usage_last_event_timestamp_seconds`) renders "No data" — the
   same thing it renders when the exporter is unreachable.
3. ADR-008's zero-fill actively manufactures the wrong reading here. For a
   provider that is available, `sum by (tool) (ai_usage_tokens) or (0 * max by
   (tool) (ai_usage_provider_available))` paints a confident `0` line. "We
   measured no consumption" and "we read the source and parsed nothing" are
   different facts that currently render identically.

The `doctor` channel is not a fallback the fleet can fall back on. JCB-353 is
blocked, at the time of writing, on exactly one missing input: `doctor --output
json` executed **on the host**. A diagnostic that requires shell access to each
of four hand-deployed machines is not an operational signal.

One invariant from `scanProvider` matters for the shape of the fix:
`tr.Available = successes > 0` and `LatestSourceModifiedAt` is only advanced
after a successful parse, so a non-zero `LatestSourceModifiedAt` and
`Available == 1` are the same condition. `staleReason` returns `""` whenever
`latestSource.IsZero()`. **A stale reason is therefore only ever defined for an
available provider**, which is what lets the new metric be scoped to available
providers without inventing a third ambiguous state.

## Decision

**Expose liveness as its own state-set metric. Do not redefine
`ai_usage_provider_available`.**

```
ai_usage_provider_stale{tool, reason} 0|1
```

- `reason` takes exactly the three values `staleReason` can return:
  `no_usage_events`, `source_newer_than_last_event`, `last_event_too_old`.
  The set is closed and owned by this exporter, not by provider data: it grows
  only by a change to `staleReason`, which must update this ADR and
  `docs/metrics.md` in the same PR.
- For every **available** provider, all three series are materialized, at most
  one of them `1`. The reasons are mutually exclusive because `staleReason` is
  a first-match chain.
- For an **unavailable** provider, no series is emitted, matching
  `ai_usage_sessions` and `ai_usage_tool_calls`.

`reason` is a label rather than a numeric encoding in the help text because the
operator needs to distinguish the three cases in an alert expression, and a
mapping that lives in prose cannot be queried.

Zero-filling the closed `reason` axis follows ADR-008: a state set whose values
are all `0` is a positive assertion of health, whereas an absent series would
make "healthy" and "not scraped" the same observation — the exact failure this
ADR exists to remove.

Together the two metrics are the provider's full health state machine:

| `provider_available` | `provider_stale` | Meaning |
|---|---|---|
| `0` | absent | Source missing, unreadable, or every parse failed |
| `1` | all `0` | Source read, events current |
| `1` | `no_usage_events=1` | Source read, zero events parsed — the `macmini` case |
| `1` | `source_newer_than_last_event=1` | Source keeps growing, the parsed stream does not — the JCB-343 signature |
| `1` | `last_event_too_old=1` | Source read and current, the tool is simply idle |

Fleet alert expression, with no dependence on series absence:

```promql
max by (instance, tool) (ai_usage_provider_stale{reason="no_usage_events"}) == 1
```

### Cardinality

5 tools × 3 reasons = **15 series per instance**, against the declared bound of
4,500 (+0.33%). No existing label is touched and no new label is added to any
usage series. `reason` applies to this metric only and must never appear on
`ai_usage_tokens`, `ai_usage_cost_usd`, `ai_usage_sessions`, or
`ai_usage_tool_calls`.

### `window="all"` across a schema generation change

ADR-009's semantics stand: the newest generation is authoritative and
generations are never summed, so `window="all"` is the available history of the
authoritative generation. That meaning is already written down — `docs/metrics.md`,
"Schema migrations and `window=\"all\"`", committed with JCB-351 on 2026-10-13 —
and this ADR does not reopen it.

What was missing was not the definition but its **observability**: nothing in
the contract marked the moment a generation flip truncated a total, so a drop
from 188M to 0 was indistinguishable from a parse regression. That is now
reported, for the drop-to-zero case, by `no_usage_events`.

A truncation to a smaller **non-zero** total remains indistinguishable from
genuinely reduced usage, by design: both are a correct reading of the
authoritative generation. The operator correlates the drop with
`ai_usage_build_info` and with the provider's own release notes. Exposing the
selected generation as a metric label was rejected (see alternatives): it would
promote a provider-internal name to public API for the one provider that has
generations today.

### Dashboards declare three states, not two

An empty panel is neither "no consumption" nor "failure" — the dashboard must
be able to render both, plus health. Consequences for the shipped dashboards:

1. ADR-008's zero-fill stays. Removing it would regress JCB-341 and reintroduce
   gaps in closed scopes.
2. A zero-filled usage panel must never be the only element that can be read
   in its row. The health row of `ai-usage-live.json` gains a
   `ai_usage_provider_stale` element next to `ai_usage_provider_available`, and
   the freshness panel's description states that "No data" means zero parsed
   events, pointing at the stale panel.
3. Gating each zero-fill expression on `unless ... ai_usage_provider_stale
   {reason="no_usage_events"} == 1` was rejected: it spreads fragile label
   matching across six or more panels, where a mistake silently drops series —
   a worse failure than the one being fixed. ADR-008's own precedent for a
   misleading empty state is a companion element in the same row, not cleverer
   PromQL.

## Consequences

- `ai_usage_provider_available` keeps its documented meaning, so ADR-007's
  degraded startup and every existing zero-fill expression keep working
  unchanged. A tool whose source is healthy but unused is still `available=1`.
- `no_usage_events` is a liveness alert, not a usage alert. On a freshly
  installed host where a tool has a source but no history yet, it fires
  correctly and must be acknowledged rather than suppressed: at that point the
  exporter genuinely cannot tell "never used" from "stopped being counted".
- `ai_usage_last_event_timestamp_seconds` stays **absent** for a provider with
  zero events. Emitting `0` would advertise 1970 and poison `time() - x` and
  every `min`/`max` over it. Its absence with `available=1` is exactly what
  `no_usage_events` reports, which is the series an alert should use.
- The three thresholds stay in code (`staleEventAge` 21d, `sourceEventLag` 7d).
  They become operationally visible for the first time, so they will now be
  judged by real fleet data; a change to either is a change to what this metric
  means and belongs in an amendment here.
- The signal is reported per scan, so it recovers on its own as soon as a scan
  parses a current event. It needs no persistent state, consistent with ADR-001.

## Implementation contract

For the follow-up implementation ticket, JCB-358; the architecture decision is
this document. Until JCB-358 ships, `ai_usage_provider_stale` is specified and
not emitted, and `docs/metrics.md` says so in the metric's own section.

- `internal/collector/collector.go`: one `prometheus.NewDesc` named
  `provider_stale` with labels `{"tool", "reason"}`; help text: `1 if tool's
  source was read but its usage is not current, for the given reason; 0
  otherwise. Emitted only for available providers.` Emit the three reasons for
  every `result.PerTool` entry with `tr.Available`, `1` for
  `tr.StaleReason`, `0` for the other two.
- Export the reason set from `internal/scan` (e.g. `scan.StaleReasons()`) so
  the collector cannot drift from `staleReason`.
- Tests: a collector test asserting 3 series per available provider with
  exactly one `1`; a test asserting 0 series for an unavailable provider; a
  test asserting that a source read with zero events yields
  `no_usage_events=1` together with `provider_available=1`; a cardinality test
  pinning 15 series for 5 tools.
- `deploy/grafana/dashboards/ai-usage-live.json`: health-row element plus the
  freshness-panel description, per the dashboard rule above.

## Alternatives considered

1. **Age gauge** `ai_usage_provider_last_event_age_seconds{tool}`. Rejected as
   both redundant and insufficient. Redundant because
   `ai_usage_last_event_timestamp_seconds` already exists and the live
   dashboard already derives the age from it with `time() - x`. Insufficient
   because with zero events there is no last event: in this codebase the series
   is not `+Inf` or `NaN`, it is simply never emitted, which is precisely the
   invisible case. It also cannot express
   `source_newer_than_last_event`, which needs source mtime, not age.
2. **Redefine `available` as "produced events".** Rejected. It breaks the
   documented contract that a rename or semantic change is a public-API change;
   it contradicts ADR-007, where a degraded scan must be distinguishable from
   an absent source; it makes an installed but idle tool read as down, turning
   `last_event_too_old` into a false outage; and it would silently change the
   meaning of every shipped zero-fill expression, which uses
   `provider_available` as the "this tool exists" witness.
3. **Document that `available` is not liveness and leave the contract alone.**
   Rejected as demonstrably insufficient. It is the current state, it is what
   produced this finding, and the diagnosis it prescribes — run `doctor` on the
   host — is the input JCB-353 is blocked on. The documentation half of this
   option is kept regardless: `docs/metrics.md` now states explicitly that
   `available` is not a liveness signal.
4. **Single boolean** `ai_usage_provider_stale{tool}` with the reason in logs
   only. Rejected: 10 fewer series is not worth losing the distinction between
   "parsed nothing" (act now) and "idle for three weeks" (probably fine),
   which is the only thing that makes the alert actionable.
5. **Emit only the active reason, absent when healthy.** Rejected: it halves
   the series count but makes healthy indistinguishable from not-scraped, and
   `absent()`-based alerting is the pattern this ADR is removing.
6. **Expose the selected schema generation as a label**, e.g.
   `ai_usage_provider_schema_generation{tool,generation}`. Rejected as
   premature. It would make a generation flip directly visible, but it promotes
   provider-internal table naming to public API, and only OpenCode has
   generations today. `no_usage_events` plus `ai_usage_build_info` already
   localize the flip to a scrape. Reconsider if a second provider grows
   generations, or if a non-zero truncation is observed in the fleet.

## Review

2027-01-15, or earlier if `no_usage_events` proves noisy on a freshly
installed host, or if a second provider introduces schema generations.
