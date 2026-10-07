# Provider: Codex CLI

Implementation: `internal/providers/codex`. `tool` label value: `codex`.

## On-disk format

| What | Path | Overridable with |
|---|---|---|
| Live sessions | `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` | `CODEX_HOME` |
| Archived sessions | `~/.codex/archived_sessions/**/*.jsonl` | `CODEX_HOME` |

Both roots are JSONL, one event per line. `CODEX_HOME`, when set, replaces
the whole `~/.codex` base for both roots - it doesn't just add to it (see
`provider.Env.HomeEnv`).

## There is no `type:"turn"` line

An earlier version of this package assumed a `type:"turn"` line carrying
`last_input_tokens` / `last_output_tokens` / `last_cached_tokens` /
`last_total_tokens`. That shape does not exist in any real Codex rollout:
verified against 275 real rollouts (and separately against the one real
rollout available on the machine this rewrite shipped from), the line types
that actually occur are `session_meta`, `turn_context`, `response_item`,
`event_msg`, `token_usage_record`, and `world_state`. Usage travels only in
`event_msg` lines whose `payload.type` is `"token_count"`.

## Line shapes this parser reads

The **first line** of a well-formed rollout is `session_meta`:

```json
{"type":"session_meta","timestamp":"...","payload":{"id":"<session-id>","session_id":"<same-id>","cwd":"..."}}
```

`Parse` uses `payload.id` as the event's `SessionID`, falling back to
`payload.session_id` when `id` is empty. If the first line isn't a valid
`session_meta` (missing, truncated, or some other event type), the
filename (without its extension) becomes the `SessionID` instead - this is
not an error.

A `turn_context` line precedes each turn and carries the model in effect for
it:

```json
{"type":"turn_context","timestamp":"...","payload":{"turn_id":"...","model":"gpt-5.5", ...}}
```

`Parse` keeps the last `model` it has seen (from `turn_context`, or from
`session_meta.payload.model` as the initial value when present) and attaches
it to every `token_count` event until a newer `turn_context` changes it. A
rollout with no model ever seen emits `"unknown"`, never an empty string.

Usage lives in an `event_msg` line with `payload.type == "token_count"`:

```json
{"type":"event_msg","timestamp":"...","payload":{
  "type":"token_count",
  "info":{
    "total_token_usage":{"input_tokens":13482,"cached_input_tokens":11648,"output_tokens":6,"reasoning_output_tokens":0,"total_tokens":13488},
    "last_token_usage": {"input_tokens":13482,"cached_input_tokens":11648,"output_tokens":6,"reasoning_output_tokens":0,"total_tokens":13488}
  }
}}
```

`response_item` lines whose `payload.type` is `function_call`,
`custom_tool_call`, or `web_search_call` are counted toward `ToolCalls` and
attributed to the next `token_count` event; only the type discriminator is
decoded, never `payload.name` or `payload.arguments`, which can carry real
file paths and shell commands.

`token_usage_record` lines are seen (one per 275 real rollouts sampled) and
explicitly ignored - negligible volume, not an error.

## `total_token_usage` is cumulative, not a delta - and so is `last_token_usage`

Both `total_token_usage` and `last_token_usage` report **running totals**,
not the token count for that turn alone. `Parse` feeds `delta.Tracker` with
`total_token_usage` and ignores `last_token_usage` entirely, for a reason
that isn't obvious from the field name: Codex has been observed emitting a
**duplicate** `token_count` for the same turn, where `last_token_usage`
repeats the same value both times. Treating `last_token_usage` as a
per-turn delta would double-count that turn. `total_token_usage`, run
through `Tracker`, naturally yields a delta of zero on the duplicate instead
- deduplication falls out of the cumulative-total math for free, with no
extra bookkeeping.

When a field in `total_token_usage` is *lower* than the previous
observation - a compaction or a fresh context window - `Tracker` treats
that drop as the counter having reset to zero, and reports the field's
current absolute value as the delta rather than a negative number
(`testdata/rollout_reset.jsonl`, `TestParseResetNeverEmitsNegativeTokens`).

## Two nestings, not one

`total_token_usage` nests two pairs of counters, following OpenAI's own
Responses API convention - not a Codex-specific quirk:

- `cached_input_tokens` is **contained in** `input_tokens` (cache hits are a
  detail of the prompt, not additional input).
- `reasoning_output_tokens` is **contained in** `output_tokens` (reasoning
  tokens are a detail of the completion, not additional output).

Verified on 1934 real `token_count` records with zero counterexamples: for
every record with `cached_input_tokens > 0`, `total_tokens == input_tokens +
output_tokens` (never `+ cached_input_tokens` again); same for every record
with `reasoning_output_tokens > 0`. See ADR-004 for the full evidence and
for why OpenCode's equivalent nesting is *not* uniform the same way.

`Parse` undoes both nestings on the **deltas**, after `Tracker`, never on
the raw cumulative fields:

```
input      = max(0, Δinput_tokens  - Δcached_input_tokens)
cache_read = Δcached_input_tokens
output     = max(0, Δoutput_tokens - Δreasoning_output_tokens)
reasoning  = Δreasoning_output_tokens
```

`model.TokenCacheWrite` is never populated for Codex. A
`cache_write_input_tokens` field has been observed in real rollouts, but
every occurrence seen is `0` and Codex has no cache-write concept of its
own to report; emitting a measured-looking `0` would hide that this class
is simply unavailable for this provider, which `docs/metrics.md` treats as
a different thing from a true zero. The key is absent from
`UsageEvent.Tokens` entirely - never present and `0`.

### Why this is the regression test that matters

`TestParseTokenClassesSumToTotalDelta` asserts, per turn, that
`input + output + cache_read + reasoning` equals that turn's own
`Δtotal_tokens`. A parser that forgot either subtraction would inflate this
sum past the turn's real total - and would still pass a test that only
checks the raw `last_*`/`total_token_usage` fields individually, which is
exactly how the previous, nonexistent-schema version of this package shipped
with green tests.

## Scale

Real Codex rollouts have been reported at 700 MB-2 GB (see issues in
`openai/codex`). `Parse` reads with a bounded line reader (`nextLine`, an
8 MiB cap) and never buffers more than one line at a time. A line over the
cap - most likely a `turn_context` with an unusually large nested
sandbox-policy payload, or corrupt input - is discarded without aborting
the rest of the file; `TestParseSurvivesOversizedLine` is the regression
test for a `bufio.Scanner` + `Buffer(...)` version of this parser, which
returns `bufio.ErrTooLong` on such a line and silently drops everything
after it. `BenchmarkParseLarge` exercises a 200 MB synthetic rollout and
asserts both a time budget (< 3s) and a live-heap budget (< 64 MiB growth)
to catch a regression back into whole-file buffering.

## Fixtures

`testdata/rollout_basic.jsonl`, `rollout_reset.jsonl`, and
`rollout_no_meta.jsonl` are built from the line shapes verified against real
rollouts (field names and nesting), with `cwd`, tool-call `name`/`arguments`,
and other free-text fields replaced with `REDACTED` or synthetic
placeholders. `rollout_basic.jsonl` includes a turn where
`cached_input_tokens > 0` **and** `reasoning_output_tokens > 0` at once -
the case where a sign error in either subtraction above would show up
immediately - plus a mid-session model switch and two attributed tool
calls. `rollout_reset.jsonl` includes one truncated line between two valid
turns, which must be skipped rather than aborting the scan.

## Out of scope for this provider

USD cost and `/metrics` exposure are handled elsewhere (pricing and
collector packages); this package only emits `model.UsageEvent` values.
