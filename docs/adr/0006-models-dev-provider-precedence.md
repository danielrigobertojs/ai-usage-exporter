#ADR-006: First-party provider precedence for models.dev

- Date: 2026-10-08
- Status: Accepted
- Fix: When an installation consumes the same model ID for two
different providers or gateways

## Context

The `models.dev` catalog publishes the same model ID under service providers.
first part and under resellers. Until now `parseModelsDev` chose the row
of the provider ID alphabetically first. On the verified installation, that
routinely chose `302ai`, a reseller line that skipped shipping fees.
`cache_read` and `cache_write`.

94% of the tokens in the 30-day window were `cache_read`, so
These omissions were valued at zero. The `ai_usage_cost_usd` metric was
$62.91 instead of $279.33: an understatement of about 4.4x.

## Decision

For bare IDs that appear under multiple providers, they are traversed first, in
order, first-party providers representing collection paths
direct from the supported tools:

```
anthropic, openai, opencode, google, xai, moonshotai, deepseek,
zhipuai, z-ai, mistral, meta, alibaba, qwen
```

Providers that are not in that list are then traversed by ID
alphabetically, preserving the previous fallback. An entry without cost
still does not create a rate: it is a priceless model, not a free model.
A 'cost' entry with all classes set to zero is still a zero rate
explicit.

## Alternatives considered

1. **Maintain the alphabetical tiebreaker.** It is simple, but in practice
selected resellers that eliminate cache fees and published a
materially false cost. Discarded.
2. **Choose the row with the most fare classes.** Avoid a maintained list,
but you can select a more expensive reseller: for `claude-fable-5`, the
Fullest row observed had input of $11/M versus $10/M of
Anthropic. Discarded.
3. **Qualify the catalog and each event by provider.** It is the attribution
correct for consumption by gateways, but requires changing the model of
data, the embedded/cache/overlay and `Catalog.Lookup` tiers. It is postponed.

## Consequences

- The estimate goes up approximately 4.4x in 30 days in the installation that
revealed the defect; It is not a usage regression but rather cache valuation
that was missing.
- `ai_usage_cost_usd` is still public list price, not an invoice:
may differ for discounts, credits and subscriptions.
- The heuristic is deterministic and preserves the alphabetic fallback for
unrecognized providers.

## Reopening condition

Replace this heuristic with provider-qualified model IDs when
a facility consumes the same model through two suppliers or
different gateways.
