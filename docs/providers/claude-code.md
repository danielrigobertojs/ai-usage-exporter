## Provider: Claude Code

`internal/provider/claudecode`. `tool` label value: `claude-code`.

### Route

```
~/.claude/projects/<project-slug>/<session-uuid>.jsonl
```

Relocatable with `CLAUDE_CONFIG_DIR` (completely replaces the resolution of
usual `$HOME`, it is not added to it). One file per session; a JSON line
per entry.

### 30 day retention — read before interpreting the `all` window

Claude Code autodelete sessions from this directory after 30 days (configurable
by the user). This is not an implementation detail: it changes what the
window `all` of this provider means.

- `all` is **not** "the entire usage history of Claude Code" — it is "whatever
still exists on disk at the time of this scan." A deleted session
by retention it stops counting for *any* window, including `all`.
- This is why [ADR-001](../adr/0001-startup-scan-and-gauges.md) models the series
of use as gauges recalculated at each start, never as counters: a
re-derived total after a retention purge may be **less** than the
served before the reset, and a counter that goes down is indistinguishable from a
reset for `rate()`/`increase()`.
- A dashboard that reads `ai_usage_tokens{tool="claude-code", window="all"}`
as "lifetime cumulative usage" is misinterpreting the metric.

### Field Mapping

Only `type == "assistant"` entries with a `message.usage` block are
relevant; everything else (user input, tool results,
compaction digests) is discarded before being fully decoded.

| JSONL | `model.UsageEvent` |
|---|---|
| `uuid` | `Key.MessageID` |
| `sessionId` (or filename without extension, if missing) | `Key.SessionID` |
| `cwd`, ​​normalized (or parent directory slug, if missing) | `ProjectID` |
| `message.model` | `Model` |
| `timestamp` (RFC3339, with or without fraction of a second), converted to UTC | `Timestamp` |
| `message.usage.input_tokens` | `Tokens[TokenInput]` |
| `message.usage.output_tokens` | `Tokens[TokenOutput]` |
| `message.usage.cache_read_input_tokens` | `Tokens[TokenCacheRead]` |
| `message.usage.cache_creation_input_tokens` | `Tokens[TokenCacheWrite]` |
| number of `tool_use` blocks in `message.content` | `ToolCalls` |

`ProjectID` only becomes the `project` tag if that tag is activated
explicitly — see `docs/metrics.md`; by default it remains unused outside
logs.

### Deduplication

Compaction and `/resume` make the same entry `assistant` (same
`uuid`) reappears in the JSONL. This provider **does not deduplicate**: it emits both
appearances with the same `EventKey`, and `internal/aggregate.Aggregator` is the
only point that rules out the repeated one. See
`internal/provider/claudecode/testdata/session_compacted.jsonl`.

### Parsing robustness

- Malformed lines, without `usage`, or with invalid `timestamp` are skipped without
abort the rest of the file — see `testdata/session_malformed.jsonl`.
- A line that exceeds 8 MiB is discarded without loading it into memory and without
stop scanning the rest of the file.
- The text of a message or the input/output of a message are never decoded.
tool: the decode struct has no field for it, so
there is no possible route for that content to reach a `UsageEvent`.
