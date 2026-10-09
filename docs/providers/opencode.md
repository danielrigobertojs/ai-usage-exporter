## OpenCode

OpenCode is the only supported tool whose history lives in SQLite instead
of JSONL: since v1.14 it stores sessions and messages in
`~/.local/share/opencode/opencode.db` (`%LOCALAPPDATA%\opencode\opencode.db`
on Windows, or wherever `OPENCODE_DATA_HOME` points), tables `session` and
`message`, with tokens and native cost living inside `message.data`'s JSON
blob rather than in their own columns.

### The size of `opencode.db` does not limit the scan

`internal/provider/discover.go`'s scan budget caps how large a candidate
file may be (`MaxBytesFile`, 256 MiB) and how many total bytes a scan may
admit (`MaxTotalBytes`, 4 GiB) - but both caps are exemptions for
`SourceSQLite`, this provider's `Descriptor.Kind`. See
[ADR-005](../adr/0005-scan-budget-by-source-kind.md) for the full
rationale; in short: those caps bound how much a *streaming* `Parse` has to
read sequentially, and this provider never streams the file. `Parse` opens
`opencode.db` through `modernc.org/sqlite` with the read-only DSN in
`DSN()` and runs the indexed query below; the driver pages the file on
demand through SQLite's own B-tree engine, never loading it into memory.
A real `opencode.db` observed in production reached 2.75 GB - 10x past
`MaxBytesFile` - and was silently dropped by `Discover` before this
exemption existed, which is exactly the "CI green, zero events on real
data" failure mode ADR-004 documents for the other two providers, one
layer further down in the scan budget instead of in the parser.

What still bounds a pathologically slow query against a huge or corrupt
`opencode.db` is the scan's `Deadline` and the `busy_timeout(2000)` baked
into `DSN()` - not the file's size.

### Query

```sql
SELECT m.id, m.session_id, m.data, s.directory
FROM message m JOIN session s ON s.id = m.session_id
WHERE json_valid(m.data) AND json_extract(m.data, '$.role') = 'assistant'
ORDER BY m.id;
```

`role` is **not** a column of the real `message` table - that table is just
`(id, session_id, time_created, time_updated, data)`, and `role` lives
inside the `data` JSON blob like everything else this provider reads. A
query that filters on `m.role` fails with SQLite's `no such column: m.role`
on every real `opencode.db`, aborting the whole scan; `json_extract` is the
only correct way to filter on it. `json_valid(m.data)` must come first:
SQLite short-circuits `AND`, and `json_extract` raises a `malformed JSON`
error instead of returning `NULL` when `data` isn't valid JSON at all -
without the guard, a single corrupt row would abort the entire query rather
than just being excluded by it. Only `role = 'assistant'` rows become
events; user and tool-role rows are read by the join but filtered out
before `m.data` is ever decoded. From `m.data` this provider extracts only:
`modelID`, `providerID`, `time.created`, `tokens.total`, `tokens.input`,
`tokens.output`, `tokens.cache.read`, `tokens.cache.write`,
`tokens.reasoning`, and `cost`. OpenCode's schema has changed across
versions, so decoding is tolerant: a missing field decodes to its zero
value, never an error, and a row whose `data` isn't valid JSON at all is
skipped rather than aborting the scan.

### Reasoning nesting

Per [ADR-004](../adr/0004-token-class-normalization.md), OpenCode does not
use one `reasoning`-vs-`output` convention across every model it talks to.
Measured over 18,423 real assistant records, 1,122 of them - all a single
`opencode-go`/`kimi-k2.5` pair - have `reasoning` counted *inside* `output`
as well as on its own, instead of disjointly. Emitting both as-is would
double-count those reasoning tokens.

The decision is derived from each record's own declared arithmetic, never
from a hardcoded model list, so a new model that adopts either convention
is covered without a code change:

```
nested := tokens.total present
          && tokens.total == input + output + cache.read + cache.write
          && reasoning > 0

output = nested ? max(0, tokens.output - tokens.reasoning) : tokens.output
```

If `total` already balances without `reasoning`, then `reasoning` travels
inside `output` and must be subtracted; `input`, `cache.read`, `cache.write`
and `reasoning` are emitted unchanged either way. When `tokens.total` is
absent (673/18,423 real records), there is no arithmetic to check, and the
default is additive (no subtraction) - correct for all 673, since 533 of
them are `opencode`/`grok-code` with `reasoning > 0` (a pair that is
additive in every record where `total` is present) and the remaining 140
have `reasoning == 0`, where the convention makes no difference.

Because the nesting decision is per record, the five-class-sum invariant
this project requires of every provider (`docs/adr/0004-token-class-normalization.md`)
is also checked per record for OpenCode, not as one aggregate sum: a record
with `total` present must have its five emitted classes sum to exactly that
total, after any nesting subtraction; a record with no `total` has nothing
to reconcile against and is excluded from the check rather than assumed to
pass or fail it.

**One documented, bounded exception to that invariant exists.** Of the
1,122 nested records, 6 declare `reasoning > output` - self-contradictory on
origin, since the same record's arithmetic says `reasoning` both fits inside
`output` (`total == i+o+cr+cw`) and is larger than it. OpenCode does not
derive the two counters from the same source for these, so neither branch of
the formula above reconciles them; `max(0, output - reasoning)` is kept
because it has the lower error bound of the two measured. See [ADR-004's
error-bound table](../adr/0004-token-class-normalization.md#the-6-arithmetically-contradictory-records)
for the measured cost of each alternative. For these 6 records only, the
five emitted classes sum to `total + (reasoning - output)`, not `total` -
fixed in the test fixture as `msg_7` (`internal/providers/opencode/testdata/make_fixture.go`,
redacted from `msg_d6498c343002a4B11hEll2WOjs`) so `TestParseFiveClassInvariant`
exercises this case explicitly instead of it disappearing silently if the
fixture ever loses it.

The native `cost` field is decoded but currently has nowhere to go:
`model.UsageEvent` carries no cost field yet, and extending that struct is
outside this provider's scope. Reconciling native cost against the pricing
catalog is tracked separately (JCB-313); for now the value is parsed and
dropped.

### Connection string

```
file:<path>?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(2000)
```

Built by `DSN(path)`. Every part of it exists to protect a database that
may be open under a live OpenCode session right now:

- `mode=ro` refuses to create the file or open it for writing at the
  SQLite level.
- `_pragma=query_only(1)` rejects any write statement even if a future code
  change accidentally issued one.
- `_pragma=busy_timeout(2000)` bounds how long a query waits on a lock a
  live writer holds, instead of hanging forever.

**Never `immutable=1`.** That flag tells SQLite the file will not change
for the lifetime of the connection, which lets it skip locking and WAL
bookkeeping entirely - correct for a baked-in read-only snapshot, actively
wrong for `opencode.db`, which a running agent can be appending to at any
moment. Using it here would risk reading a torn, inconsistent view of the
database, or worse.

### WAL note

`opencode.db` is routinely in WAL mode. This provider never checkpoints,
never creates a `-wal`/`-shm` file that wasn't already there, and never
changes a single byte of the main database file - verified by a dedicated
read-only test that hashes the file before and after `Parse` and asserts
the directory gained no journal file.
