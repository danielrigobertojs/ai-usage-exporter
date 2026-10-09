#ADR-005: Byte scan budget does not apply to SQLite sources

- Date: 2026-10-07
- Status: Accepted
- Fix: if a queryable format (non-JSONL) appears where the size of the
file does predict the cost of the query

## Context

`internal/provider/discover.go` applies two byte caps to every candidate
which finds, without distinguishing `SourceKind`: `MaxBytesFile` (256 MiB per
file) discards any candidate that exceeds it, and `MaxTotalBytes`
(4 GiB) stops supporting candidates once the cumulative sum of sizes
surpasses them.

Measured on 2026-10-07 on `main` at `f737887` (with JCB-321 and JCB-322 already
merged) against the actual data of `Daniels-MBP.lan`:

```
claude-code  files=30   skipped=0   events=2507    tokens=309451691   parse_errors=0
codex        files=275  skipped=0   events=1934    tokens=57828243    parse_errors=0
opencode     files=1    skipped=1   events=0       tokens=0           parse_errors=0
```

The OpenCode provider does not have any parsing bug — JCB-322 already corrected
your query — but it never gets executed: `opencode.db` weighs 2.75 GB in
that machine, 10.2x above `MaxBytesFile`, and `Discover` discards it
before `Parse` sees it. `skipped=1` with `events=0` is the exact signature
of this ruling.

## Why byte cap is the wrong control for SQLite

`MaxBytesFile` exists to limit how much a `Provider.Parse` has to read
*in streaming* before broadcasting the first event — see comment by
`Budget` in `discover.go`: without it, a 2 GB JSONL converts serving the
first `/metrics` in a wait of several minutes. For a format that
reads sequentially from start to finish, file size **is**
literally the work that `Parse` is going to do, so it's the control
correct.

A `SourceSQLite` source is not read like this. `opencode.Parse` opens the file
with `modernc.org/sqlite` in `mode=ro` mode and run an indexed query
(`SELECT ... FROM message JOIN session ... ORDER BY m.id`); the driver page
the file on demand through the SQLite B-tree engine, it never loads the
Full `.db` in memory. The disk size of an `opencode.db` does not predict
nor how much memory that query uses nor how long it takes — what matters is
how many rows of `assistant` role does it contain and how fragmented is the
index, neither derivable from `info.Size()`.

Counting those bytes also produced a second wrong effect:
`MaxTotalBytes` (4 GiB) was consumed at ~69% with this single file,
leaving the *other* providers of the same scan without a budget — although
Today each call to `Discover` starts from its own `totalBytes` per invocation
and `scan.Run` calls `Discover` once per provider, so this
The second effect is not yet manifested across providers.
It is still the wrong dimension to measure the cost of a source that
will not be read in full, and a future implementation that shares
budget between providers in the same scan would inherit the problem if not
It is corrected here first.

## Decision

**`SourceSQLite` sources are exempt from the two byte caps**
(`MaxBytesFile` and accounting for `MaxTotalBytes`) in
`internal/provider/discover.go`. `Deadline` and `MaxFiles` still apply
without exception — are the actual guard for this font, not the size of the
file.

```go
if d.Kind != SourceSQLite && info.Size() > b.MaxBytesFile {
    // ... descartar
}
...
countsTowardTotal := c.Kind != SourceSQLite
```

Formats that are read in streaming (`SourceJSONL`, `SourceJSON`) are not
they change: they remain bounded by both stops exactly as before.

## Alternatives considered

1. **Exempt `SourceSQLite` from byte caps (chosen).** Minimal,
directed, restores the provider on real data, and leaves control in
the dimension that does correspond to this source (time, via `Deadline`).
Disadvantage: a pathological database (corrupt indexes, a disk of
very slow network) could make the query slow; it is limited by the `Deadline`
of the budget and the `busy_timeout(2000)` that the DSN already has
`opencode.DSN`.
2. **A much larger limit for SQLite** (e.g. `MaxBytesSQLite`,
16 GiB). Keeps an explicit guard for size, but it is a number
arbitrary that a future database can again exceed, and continues
measuring a dimension that is not the real cost of a source consulted
by index. Discarded.
3. **Budget by `SourceKind`** (a `map[SourceKind]int64` of caps).
The most general — it would cover a future format with its own profile of
cost without touching `Discover` again — but it's surface
new configuration for today's only real need (an exempt kind,
not several different stops). Early. It is reconsidered if a
second format with its own cost profile.
4. **Leave it as is.** Discarded: Publish an MVP where one of the three
providers scope is permanently mute about actual data,
something that already failed twice before (JCB-321, JCB-322) for the same reason
root — “Green IC, zero events on real data” — one level down,
in the scan budget instead of in the parser.

## Consequences

- `Discover` can now return a `SourceSQLite` source from any
size; `Provider.Parse` is still responsible for parsing its own
work (in OpenCode, via `busy_timeout(2000)` in the DSN and respecting
`ctx` in the row loop).
- `MaxTotalBytes` stops reflecting "total bytes of everything found" and
now means "total bytes of sources read in streaming";
an SQLite source never contributes to or consumes that accumulation.
- A future change that shares `Budget`/`MaxTotalBytes` between providers
within the same `scan.Run` (today each call to `Discover` starts from a
own accountant) inherits this exemption automatically, without having to
rediscover the problem of JCB-324.
- This decision does not reopen or modify ADR-001: it still has not been
aggregate persisted between boots; this is exclusively about what
counts as "bytes to budget" within a single scan.

## Reopening condition

Reopen this decision if a searchable format appears (not streamed)
where the size of the file on disk does reliably predict the cost of
read it — in that case, go to alternative 3 (budget by
`SourceKind`) instead of adding one more ad-hoc exemption.
