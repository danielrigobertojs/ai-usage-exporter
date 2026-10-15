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

For OpenCode, this means that once a current-shaped, joinable session/message
pair exists, the provider queries that pair alone. It does not fall back to
legacy merely because the new generation is empty, and it does not append
legacy-only history. The accepted limitation is the measured loss of 57
assistant events (5,553,025 tokens) that the migration did not carry forward.
Recovering those events would require a separate decision and implementation;
it must not be recovered incidentally by a union.

`TestParseSchemaGenerationAuthority` supplies both generations with a shared
message ID and identical token payload, then also a legacy-only message. It
asserts that the report has exactly the current-generation total. A change
that joins or accumulates both table families fails this test.

## Consequences

- OpenCode `window="all"` means the available history in the authoritative
  generation, not an all-time union of tables that happen to survive on disk.
  A migration can therefore make the provider's total decrease without a
  defect in aggregation.
- The selected current generation may add newer events and models, increasing
  the OpenCode series actually emitted. This remains within the documented
  cardinality model; no labels are added by this decision.
- The 0.35% historical omission is explicit and bounded. It is not a parser
  bug and is not silently repaired by future schema-selection changes.

## Amendment 2026-10-15 — the legacy generation is identified by name *and* shape

JCB-354 was asked whether JCB-345's criterion — detect a generation by
structure, never by a versioned table name — reopens for the legacy pair, since
JCB-353's fix matches it as `table.name == "message" && hasColumns(columns,
"id", "session_id", "data")`.

**It does not reopen. Legacy keeps its literal names, and the name must always
be conjoined with a column check. Current generations stay structural.**

The two criteria guard opposite risks, which is why they are allowed to differ:

- JCB-345 guards an **open** set. A future generation can arrive under a name
  nobody enumerated (`session_v3`, `session_message_v3`), and selecting by name
  would silently fall back to older tables. Only structure is knowable in
  advance there.
- Legacy is a **closed** historical fact. `session`/`message` already exist on
  disk in shipped databases, and no new legacy generation can appear: legacy is
  by definition the family that predates the current one.

The decisive reason is that at the legacy shape, structure does not
discriminate. The legacy signature `(id, session_id, data)` is a strict subset
of OpenCode's delivery queues: `session_inbox` is
`(id, session_id, type, data, delivery, enqueued_seq, time_created)` and
`session_pending` is the same shape with `admitted_seq`. Structure-only legacy
selection would therefore accept a queue as the legacy message table — and
because `session_inbox` carries `time_created`, JCB-353's recency comparison
would then be able to rank a delivery queue as the "live legacy generation".
That is a worse failure than the one JCB-345 prevented, and the literal name is
the only thing that rules it out. This is the same residual risk `schemaQuery`
already documents for the current shape, where `seq` is what separates a
message table from a queue; legacy has no `seq`, so it has no structural
separator at all.

Name-based legacy identity also predates JCB-345 and was not changed by it:
`legacyQuery` is a constant that reads `FROM message m JOIN session s` and
`FROM part p` directly. JCB-353 makes an assumption explicit that was already
compiled into the SQL; making it structural would mean parameterizing that
query's table names, which buys nothing against a closed set and costs the only
discriminator that works.

Rules this fixes for future changes:

1. A legacy name check never stands alone. `message` requires
   `(id, session_id, data)` and `session` requires `(id, directory)`; a
   database that reuses either name for a different shape is rejected, not
   misread.
2. The current family is never selected by name. Adding a literal
   `session_message_v4` to `schemaQuery` is a violation of JCB-345 regardless
   of this amendment.
3. Reopen structural detection for legacy if OpenCode ships a *new* table
   literally named `message` with a different shape, or if the legacy family
   acquires more than one on-disk name. At that point the discriminator must be
   a column set that no queue table satisfies — not the legacy signature above.

Noted, not decided here: `legacyQuery` also reads `part`, which the legacy
predicate does not verify. A legacy pair without `part` makes the query fail
per source, which surfaces as `ai_usage_scan_errors > 0` rather than as silent
emptiness, so it is loud under the ADR-010 signals and is at most optional
hardening.

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
