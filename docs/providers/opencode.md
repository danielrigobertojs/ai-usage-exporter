## OpenCode

OpenCode is the only supported tool whose history lives in SQLite instead
of JSONL: since v1.14 it stores sessions and messages in
`~/.local/share/opencode/opencode.db` (`%LOCALAPPDATA%\opencode\opencode.db`
on Windows, or wherever `OPENCODE_DATA_HOME` points), tables `session` and
`message`, with tokens and native cost living inside `message.data`'s JSON
blob rather than in their own columns.

### Query

```sql
SELECT m.id, m.session_id, m.data, s.directory
FROM message m JOIN session s ON s.id = m.session_id
WHERE m.role = 'assistant'
ORDER BY m.id;
```

Only `role = 'assistant'` rows become events; user and tool-role rows are
read by the join but filtered out before `m.data` is ever decoded. From
`m.data` this provider extracts only: `modelID`, `providerID`,
`time.created`, `tokens.input`, `tokens.output`, `tokens.cache.read`,
`tokens.cache.write`, `tokens.reasoning`, and `cost`. OpenCode's schema has
changed across versions, so decoding is tolerant: a missing field decodes
to its zero value, never an error, and a row whose `data` isn't valid JSON
at all is skipped rather than aborting the scan.

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
