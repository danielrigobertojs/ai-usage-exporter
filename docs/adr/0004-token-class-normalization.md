#ADR-004: Normalization of token classes by provider

- Date: 2026-10-06
- Status: Accepted
- Fix: when adding a new provider, or if an upstream provider changes the
nesting your counters
- Updated 2026-10-07: OpenCode convention is not uniform; see the
evidence from `opencode-go`/`kimi-k2.5` below

## Context

`docs/metrics.md` establishes the invariant that the five classes of
`token_type` (`input`, `output`, `cache_read`, `cache_write`, `reasoning`)
**partitions** the tokens of an event: each token is counted in exactly
a class The invariant exists because `pricing.CostUSD` sums per class of
independently, so a token counted twice is cashed twice,
two different rates.

What the contract did not say is that **each provider uses a convention of
nesting different in origin**, and that two of the three are incompatible
each other. This was not detected during JCB-310/311/312 because each provider is
validated against synthetic fixtures written by the same author as the parser:
the fixture encoded the same assumption as the code, and the test passed.

The two non-Claude Code providers turned out to not emit any events
on real data (JCB-311, JCB-312), and when checking the real scheme
the conventions below appeared.

## Decision

**Normalization to disjoint classes is the responsibility of the provider, not the
aggregator or collector.** Each `Parse` emits `model.UsageEvent.Tokens` already
partitioned; nothing downstream re-interprets, subtracts or deduces.

Conventions verified at origin, with the evidence that supports them:

| Provider | `cache` vs `input` | `reasoning` vs `output` | Counter font |
| --- | --- | --- | --- |
| Claude Code | disjoint in origin | n/a | native API fields in JSONL |
| Codex | **nested**: `cached_input_tokens` ⊂ `input_tokens` | **nested**: `reasoning_output_tokens` ⊂ `output_tokens` | `event_msg`/`token_count`, accumulated per session |
| OpenCode | disjoint in origin | **depends on model**: disjoint in 8893/10015 records, nested (`reasoning` ⊂ `output`) in 1122/10015, all of them `opencode-go`/`kimi-k2.5` | `message.data`, delta per message |

Codex evidence, about 1919 `token_count` records of 191 real rollouts,
no counterexamples: `total_tokens == input_tokens + output_tokens` in 1919/1919
with `cached_input_tokens > 0`, and `cached_input_tokens > input_tokens` set to 0
cases; `total_tokens == input_tokens + output_tokens` in 1913/1913
records with `reasoning_output_tokens > 0`.

Independently verified on 2026-10-07 on **275** rollouts
actual available (1934 `token_count` records, 57,828,243 tokens): the sum
of the four classes that the parser emits exactly equals one
`Δtotal_tokens` recomputed separately from raw JSON, with **0**
discrepancies, 0 events with unknown model and 0 sessions without id.

OpenCode evidence, on 18,423 assistant messages from a real database,
with the arithmetic 100% explained and without a single unclassified case:

```
assistant_all                                      18423
  sin campo tokens.total                             673
  reasoning == 0,  total == i+o+cr+cw               7735
  reasoning  > 0,  total == i+o+cr+cw+reasoning     8893   <- reasoning ADITIVO
  reasoning  > 0,  total == i+o+cr+cw               1122   <- reasoning ANIDADO
  sin explicar                                         0
```

The 1122 nested records are **a single** provider/model pair,
`opencode-go`/`kimi-k2.5`, and for that pair the nesting is deterministic:
1122/1122 of your records with `reasoning > 0` and `total` present are
nested, 0 additives. The other three base providers (`openai`,
`opencode`, `omlx`) are additive in 7865/7865 of your registers with
`reasoning > 0`.

Of the 673 records without `tokens.total`, 533 are `opencode`/`grok-code` with
`reasoning > 0` — an additive provider on all its measurable records — and the
The remaining 140 have `reasoning == 0`, where the convention is indifferent. He
Additive default when `total` is missing is therefore correct in 673.

Therefore Codex **subtracts** and OpenCode **does not**:

```
# Codex: undo both forms of nesting in cumulative deltas
input      = max(0, Δinput_tokens  - Δcached_input_tokens)
cache_read = Δcached_input_tokens
output     = max(0, Δoutput_tokens - Δreasoning_output_tokens)
reasoning  = Δreasoning_output_tokens

# OpenCode: map directly unless reasoning is nested, which is decided
# per record from the arithmetic declared by that record
nested := tokens.total presente && tokens.total == i + o + cache.read + cache.write && reasoning > 0
input       = input
cache_read  = cache.read
cache_write = cache.write
reasoning   = reasoning
output      = nested ? max(0, output - reasoning) : output
```

### The 6 arithmetically contradictory records

Independently verified on 2026-10-07 on the same real basis
(18,423 wizard messages): Of the 1,122 nested records, **6 declare
`reasoning > output`** — for example `output=85, reasoning=88, total=94158`
with `total == i+o+cr+cw`. Those six are self-contradictory in origin:
`reasoning` cannot be contained in an `output` smaller than it, and at the same time
`total` does not include it. OpenCode does not derive both counters from the same
source, and neither of the two branches of the formula reconcile the record:

| Branch | `output` emitted | Sum of the 5 classes | Error vs `total` |
| --- | --- | --- | --- |
| Nested with `max(0, ...)` | 0 | `total + (reasoning - output)` | +1 to +42 tokens (63 total) |
| Additive (without subtracting) | `output` | `total + reasoning` | +85 to +625 tokens (1719 total) |

The nested branch is maintained with the `max(0, ...)`: it is the highest error level
low of the two (63 tokens out of 1,354,709,078 issued, 4.7e-8), and not
introduces a special case that a new model would have to remake
discover. The consequence is that **the log invariant admits this
documented and bounded exception**: a nested record with
`reasoning > output` sums `total + (reasoning - output)`, not `total`. A test
of an invariant that ignores it fails on real data; one who treats her like
approved without further ado loses the signal if the case grows. The correct way to
fixing it is an explicit fixture with that arithmetic (JCB-323).

OpenCode rule is derived from the registry, not a list of models
handheld: if `total` already fits without `reasoning`, then `reasoning`
it travels inside `output` and must be subtracted. A new model with the convention
nested is covered without touching code. When `total` is missing there is no signal, and
the default is additive (not subtracting), which is the 7865/7865 register convention
of additive providers.

Codex does not expose `cache_write`; is ignored instead of being output as 0, so that
the absence of the data is distinguished from a measured value of zero.

## Alternatives considered

- **Normalize in the aggregator, with a convention flag per provider.**
It centralizes the subtraction in one place, but moves specific knowledge of the
format outside the only package that already has it, and forces
`UsageEvent` carries a non-disjoint intermediate state that violates the
invariant of the contract while traveling. Discarded.
- **Add a class `input_total` that includes caching.** It would do the sums
by provider easier to compare with native UIs, but breaks
explicitly the partition, which is the property on which the cost depends.
Discarded.
- **Maintain current status.** Not an option: today the main number
of the exporter would be incorrect for two of the three MVP providers.

## Consequences

- Each provider carries a mandatory invariant test: the sum of the
five classes issued must equal the total declared by the tool
for that session. It is the only test that detects double counting, and it is the one that
was missing on JCB-311 and JCB-312. **In OpenCode the invariant is evaluated by
record, not aggregate**: the declared total is only reconcilable knowing
whether that record is nested or additive, and 673 records do not declare `total`
at all. A test that adds everything and compares it with the sum of `total`
fails on real data for 6.3% of the records, and "fix" it with a
Uniform subtraction reintroduces double counting in the other 93.7%.  He
register invariant has a single documented and bounded exception:
the 6 registers with `reasoning > output` described above.
- Provider fixtures must be **written extracts from real data**,
not synthetic. A fixture written from the same assumption as the parser does not
prove nothing; that is the root cause of the two defects. Writing means
replace `cwd`, ​​instructions, tool arguments and contents of
messages by `REDACTED`, keeping only timestamps, ids, model and
accountants.
- A new provider cannot be terminated with green tests: it is necessary
a pass on real data that shows `events > 0`. The subcommand
`doctor` (JCB-315) exposes events emitted and tokens summed by provider
precisely so that "I found files and issued 0 events" is visible in
instead of being a silent zero.
- `docs/metrics.md` does not change: the disjunction invariant was correct. It
What was missing was this mapping by provider.
