# ADR-001: Startup scans and gauges instead of counters

- Date: 2026-10-02
- Status: Accepted
- Review: reconsider if continuity of the time series across restarts is needed (see "Reopening condition")

## Amendment 2026-10-08 — live rescanning enabled by default

JCB-328 changes the default `scan_interval` from `0` to `60s`. It does not add
persistent state, offsets, or accumulation between scans: each iteration reads
the complete available history again and replaces the snapshot with a new one.
Consequently, a restart can still cause a discontinuity, and this amendment
neither satisfies nor brings forward the reopening condition for time-series
continuity or historical retention.

The evidence supporting the new cadence is a 2026-10-08 measurement on macOS
arm64 with Go 1.27.1 and `CGO_ENABLED=0`: after warming the cache, scanning 40
Claude Code JSONL files, 276 Codex JSONL files, and a 2.7 GB OpenCode SQLite
database took 0.68 s. A scan every 60 s occupies approximately 1.1% of the
cycle; an incremental watcher would save little while adding state that this
decision rejects. `scan_interval: 0` remains valid for users who need a frozen
snapshot.

## Context

`ai-usage-exporter` reads the local logs of AI agents (Claude Code, Codex CLI,
OpenCode, and others in the future) and exposes usage metrics at a `/metrics`
endpoint for Prometheus to scrape.

Those logs are not a continuous stream that the exporter can follow simply and
consistently with an incremental offset across providers: they are growing
JSONL files, SQLite databases in WAL mode, and, for Claude Code, a directory
that **automatically deletes entries after 30 days**. A message can also
reappear because of compaction, `/resume`, or session forks (see the message-ID
deduplication invariant).

Given that substrate, two related design decisions must be made before writing
any provider:

1. When should logs be read: once at startup, or continuously with persistent
   state between reads?
2. Which Prometheus metric type models "tokens used in the 7-day window": a
   monotonic counter, or a gauge recalculated at each scan?

## Decision

**The binary parses the complete logs at startup and, by default, every 60
seconds.** There is no filesystem watcher or persisted aggregate state between
starts (neither on disk nor in its own database). Each complete rescan replaces
the snapshot served at `/metrics`.

**All usage series are exposed as gauges aggregated by window** (`1h`, `24h`,
`7d`, `30d`, `mtd`, `all`), calculated at scan time from the complete history
available on disk — never as counters with a `_total` suffix. Freshness gauges
(`ai_usage_scan_timestamp_seconds`, `ai_usage_last_event_timestamp_seconds`)
let the operator determine how old the served snapshot is.

### Why a counter is the wrong model here

A Prometheus counter is valid only if Prometheus can assume that, except for a
restart of the process exposing it, it **never decreases**. `rate()` and
`increase()` rely on that invariant to detect resets.

That invariant does not hold here even within the lifetime of a continuously
running process, because the process does not rescan — but it is guaranteed to
break **between** starts: if the exporter restarts (deployment, crash, or
container restart) after Claude Code has purged JSONL files older than 30 days,
the total re-derived from the on-disk history at the second startup may be
**lower** than the total served just before the restart. Prometheus would
interpret that decrease as a counter reset (correct for a process that reset
its internal counter to zero, but incorrect for one that remeasures a partly
pruned history), and `rate()` would produce spurious spikes or negative values
clamped to zero — in other words, garbage.

A gauge has no such problem: "the total tokens in the `7d` window according to
the local history at the time of this scan" is true at every startup, even if
the history supporting it changed between one start and the next. A gauge never
promises monotonicity, so there is no contract to break.

## Rejected alternatives

**Counters plus persistent state in the exporter's own SQLite store.** This
would make it possible to accumulate a genuinely monotonic total independently
of purging source logs, and it would be the correct design if time-series
continuity beyond the raw logs' retention were needed. It is rejected for this
project because:

- It introduces mutable state that the exporter must keep consistent with a
  source it does not control (each agent's logs), including every migration or
  corruption failure mode of that store.
- It contradicts the non-destructive-reading and simplicity invariant: the
  exporter stops being a "stateless snapshot" and becomes a system with its
  own database to operate, back up, and version.
- There is currently no product requirement for historical series beyond what
  the logs themselves retain (`all` already covers "everything the local
  history still contains").

## Consequences

- Grafana dashboards that consume these metrics must use gauges directly
  (`ai_usage_tokens{window="7d"}`), not `rate()` or `increase()` over them —
  they are snapshots, not accumulators.
- Restarting the process makes the served `/metrics` change immediately to the
  new snapshot; there is no smooth transition or interpolation.
- The oldest data that `/metrics` can reflect is exactly what the source log
  still retains on disk at scan time. If Claude Code has already deleted a
  session, that session no longer exists for any window, including `all`.
- Providers must deduplicate by message ID when building the snapshot (not by
  log line), so compaction and forks do not inflate totals in one parsing pass.
- The snapshot refreshes with a complete scan every 60 seconds by default.
  Setting `scan_interval: 0` preserves frozen-snapshot behavior; `SIGHUP`
  requests an additional rescan.

### Refresh mechanism: SIGHUP and `scan_interval`

JCB-314 adds a rescan trigger and JCB-328 changes the default cadence to a
full scan every 60 seconds without contradicting the decision above: every
execution remains a complete, stateless snapshot. There are two ways to
request an additional scan:

- Send `SIGHUP` to the running process.
- Configure `scan_interval`; its default is `60s`, and `0` disables the ticker.

Each trigger runs the full `scan.Run` again and replaces the published
snapshot — there is no incremental state or offset between scans, just as at
startup. A trigger received while a scan is already in progress is discarded,
never queued: a burst of signals during a slow scan collapses to at most one
additional rescan, not one per signal. This does not reopen the gauge decision:
each rescan remains a complete snapshot of the history available at that
instant, never an accumulator across scans.

## Reopening condition

Reopen this decision if a real requirement appears for time-series continuity
across restarts (for example, trend-based alerts that cannot tolerate a restart
discontinuity, or a need to retain history beyond what source logs preserve)
**and** the team accepts the operational cost of the dedicated store described
above.
