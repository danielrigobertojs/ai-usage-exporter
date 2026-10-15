# ADR-009: The newest OpenCode schema generation is authoritative

- Date: 2026-10-13
- Status: Accepted
- Fix: when OpenCode leaves multiple schema generations in one database

## Context

OpenCode migrations retain the legacy `session`/`message` tables beside a
newer session and message-table generation. They are not independent
histories: on a measured database, 20,107 message IDs occurred in both
generations, and the token payload for the same ID was identical.

The overlap makes a union incorrect. Summing both generations would double
count about 1,354,709,078 tokens in that database, inflating OpenCode's
`window="all"` total by more than 80%. The newer generation also contained
4,414 IDs not present in the legacy generation, so reading only the legacy
tables is not a safe alternative.

The legacy table had 60 IDs absent from the newer generation; 57 were
assistant messages with 5,553,025 tokens. This is 0.35% of the measured
OpenCode total. The migration was therefore lossy at that boundary, but a
small, measured omission is preferable to inventing a much larger total by
duplicating shared messages.

## Decision

**Use only the newest recognized schema generation. Never sum generations.**

More generally, when a provider keeps more than one generation of a message
schema, the newest generation is authoritative for a message ID. A provider
must select an authority; it must not merge payloads from generations.

For OpenCode, a current-shaped, joinable session/message pair becomes
authoritative when it contains at least one `assistant` row. It does not append
legacy-only history once that condition holds. The accepted limitation is the
measured loss of 57 assistant events (5,553,025 tokens) that the migration did
not carry forward. Recovering those events would require a separate decision
and implementation; it must not be recovered incidentally by a union.

### Empty-current exception (JCB-353)

A current-shaped pair with zero `assistant` rows is not yet authoritative. If
no recognized current pair has an assistant row and the legacy `session`/
`message` pair does, the provider reads the legacy pair alone. It does not sum
the two generations. If neither family has an assistant row, it preserves the
empty current snapshot.

This exception was added after a fleet host moved from 188,772,982 tokens to
zero immediately after deployment: its `session_message` table existed but had
no rows, while `message` still held the active assistant usage. That is a 100%
loss, not the 0.35% bounded omission measured above. The exception reverses as
soon as any recognized current pair receives an `assistant` row; from that
point the current generation alone is authoritative again.

`TestParseSchemaGenerationAuthority` supplies both generations with a shared
message ID and identical token payload, then also a legacy-only message. It
asserts that the report has exactly the current-generation total. A change
that joins or accumulates both table families fails this test.

## Consequences

- OpenCode `window="all"` means the available history in one authoritative
  generation, not an all-time union of tables that happen to survive on disk.
  Before the current generation has assistant usage, that authority remains
  legacy; once it does, a migration can make the total decrease without a
  defect in aggregation.
- The selected current generation may add newer events and models, increasing
  the OpenCode series actually emitted. This remains within the documented
  cardinality model; no labels are added by this decision.
- The 0.35% historical omission after a populated migration is explicit and
  bounded. A zero-assistant current table is an incomplete migration, not that
  omission, and is handled by the empty-current exception without a union.

## Privacy invariant

OpenCode's current `session_message.data.content` can contain prompts,
reasoning, tool output, absolute home paths, and private project filenames.
`messageData` remains a closed allowlist: it must never decode `content`.

Any new field added to `messageData` must be validated against the privacy
boundary, including a test that the longest representative `content` text
cannot appear, in full or in an 80-character fragment, in any user-facing
output (`doctor`, `providers`, or either `report` window). Reviewing only the
known safe fields is insufficient: the proof is absence of the sensitive
content from every output route.

## Alternatives considered

1. **Sum both generations.** Rejected because shared IDs duplicate the same
   payload and inflate the measured total by more than 80%.
2. **Use legacy as the authority.** Rejected because it omits 4,414 newer
   message IDs.
3. **Merge by message ID.** Rejected for now. It could recover the 57
   legacy-only assistant events, but changes the defined meaning of a schema
   migration and needs its own data-integrity and privacy review.
