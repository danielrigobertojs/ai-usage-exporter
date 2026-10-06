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

The **first line** of a well-formed rollout is a `session_meta` record:

```json
{"type":"session_meta","timestamp":"...","payload":{"id":"<session-id>","cwd":"..."}}
```

`Parse` uses `payload.id` as the event's `SessionID`. If the first line isn't
a valid `session_meta` (missing, truncated, or some other event type), the
filename itself becomes the `SessionID` instead - this is not an error.

Every following line is a **turn** event:

```json
{"type":"turn","timestamp":"...","payload":{"id":"<turn-id>","model":"gpt-5-codex","last_input_tokens":250,"last_output_tokens":60,"last_cached_tokens":10,"last_total_tokens":310}}
```

## The `last_*` counters are cumulative, not deltas

This is the detail that makes Codex the easiest provider to get wrong:
`last_input_tokens`, `last_output_tokens`, `last_cached_tokens`, and
`last_total_tokens` are **the running total for the session's current
context window as of that turn**, not the token count for that turn alone.

Example from `testdata/rollout_basic.jsonl`:

| Turn | `last_input_tokens` (as logged) | Correct per-turn delta |
|---|---|---|
| 1 | 100 | 100 |
| 2 | 250 | 150 |
| 3 | 400 | 150 |

Summing the raw `last_input_tokens` column (100 + 250 + 400 = 750) inflates
the real usage (400) nearly twofold. `delta.Tracker` (`internal/providers/codex/delta.go`)
holds the previous observation per session and emits the increment instead:
`codex_test.go`'s `TestParseBasicReconstructsTotalsWithoutInflation` asserts
that the sum of the deltas `Parse` emits equals the last turn's cumulative
total exactly - that equality is the regression test for this bug.

When a field in a later turn is *lower* than the previous observation - a
compaction or a fresh context window - `Tracker` treats that drop as the
counter having reset to zero, and reports the field's current absolute
value as the delta rather than a negative number. See
`testdata/rollout_reset.jsonl` and `TestParseResetNeverEmitsNegativeTokens`.

## Scale

Real Codex rollouts have been reported at 700 MB-2 GB
(see issues in `openai/codex`). `Parse` reads with a `bufio.Scanner`
(8 MiB line buffer) and never buffers more than one line at a time;
`BenchmarkParseLarge` exercises a 200 MB synthetic rollout and asserts both
a time budget (< 3s) and a live-heap budget (< 64 MiB growth) to catch a
regression back into whole-file buffering.

## Out of scope for this provider

USD cost and `/metrics` exposure are handled elsewhere (pricing and
collector packages); this package only emits `model.UsageEvent` values.
