# Price catalog (`internal/pricing`)

`internal/pricing` resolves USD fees per token for a pair
(`tool`, `model`) and applies them to the counters of a `model.UsageEvent`
to produce `ai_usage_cost_usd`. This document is the source of truth
of that package: any changes to the resolution order, the format of
pricing files or routes by platform must be updated in the
same PR.

**What this metric shows is an estimate at the public list price in
USD from first party supplier, not an invoice.** Does not reconcile
contractual discounts, subscription plans, credits, rounding by
provider, nor the native `cost` field that some formats (e.g.
OpenCode) already include — that reconciliation is a decision of the collector who
consumes this package, documented there.

## Resolution order

```
user overlay  >  models.dev cache  >  embedded table
```

`Load` resolves in that order and **never returns an error** for a cause of
network, cache, or data: each level that fails silently degrades the
following, and the embedded table (compiled into the binary via `go:embed`) is
the ground that guarantees that there is always a price to serve, or a
explicit absence (`Lookup` returns `false`) instead of a false `0`.

`Catalog.Source()` reports which is the highest level that has data
loaded — `"overlay"`, `"models.dev"` or `"embedded"` — not the provenance
of each individual model. If the user overlay redefines a single
model, `Source()` already reports `"overlay"` although the rest of the models are
continue solving at lower levels.

Within each level, `Lookup(tool, modelID)` first tests the key
naked of the model (`modelID`) and only if it does not exist it falls to the key
qualified by tool (`tool:modelID`). A model absent in all three
levels returns `(Rates{}, false)`: an unknown model **not valid
$0**, should remain observable as "no price".

## models.dev cache

- Source: `https://models.dev/api.json` (MIT dataset of `sst/models.dev`;
see `NOTICE`).
- Local cache in `<Config.CacheDir>/models-dev-v1.json`. Default,
`Config.CacheDir` is `os.UserCacheDir()/ai-usage-exporter`.
- Default 24h TTL (`Config.TTL`), validated against the `mtime` of the
cache file — no additional metadata.
- If the network refresh fails (connection error, status other than 200,
non-parseable body) and a previous cache exists, **that cache is still
serving** even if it is expired. Only if there is no usable cache
falls to the embedded table.
- A corrupt cache (invalid bytes) is treated the same as a cache
absent: it is ignored and the next level is attempted, it never panics.
- The request sends `User-Agent: ai-usage-exporter/<version> (+<repo>)`,
as an identifiable courtesy towards whoever maintains the free dataset.
- When a model ID appears under multiple providers, a list is preferred
ordered from first-party providers (`anthropic`, `openai`, `opencode`,
...); if none match, the provider with ID alphabetically first wins.
It is a provenance heuristic, not a billing attribution. See
ADR-006.

### Force offline mode

`Config.Offline = true` prevents `Load` from making any network requests: use
the local cache if it exists (even expired) or falls directly to the table
embedded Useful for testing, air-gapped environments, or to avoid the cost
of a network call at each start when the operator prefers to refresh
the price manually.

## The user overlay

The overlay has **the same format** as the embedded table, so that a
user can copy `embedded.json` and edit it:

```json
{
  "version": 1,
  "models": {
    "claude-opus-5": {"input": 15.0, "output": 75.0, "cache_read": 1.5, "cache_write": 18.75}
  }
}
```

Values ​​are **USD per million tokens** (same as models.dev);
`Load` divides them by 1e6 when constructing `Rates`. A model that the overlay does not
mentioned continues to be resolved by the lower levels — the overlay
it overwrites by model, it does not replace the entire catalog.

### Overlay path by platform

By default, `Config.OverlayPath` is `<xdg_config>/ai-usage-exporter/pricing.json`,
resolved with `os.UserConfigDir()` — never a hand-built `~` path:

| OS | Typical route |
|---|---|
| Linux | `$XDG_CONFIG_HOME/ai-usage-exporter/pricing.json` (or `~/.config/...` if the variable is not defined) |
| macOS | `~/Library/Application Support/ai-usage-exporter/pricing.json` |
| Windows | `%AppData%\ai-usage-exporter\pricing.json` |

A missing overlay is not an error condition: `Load` simply does not
has nothing to overlap. A present but corrupt overlay is ignored
the same way as a corrupt cache.

## Token Classes and `Rates`

`Rates` are USD per **one** token (never per million), with a field
independent per class: `Input`, `Output`, `CacheRead`, `CacheWrite`,
`Reasoning`. `CostUSD` sums each event token class against its
corresponding fare independently — never mix classes or
applies a single fee to all tokens.

## Attribution

Pricing data is derived from `sst/models.dev` (MIT license). The
mandatory attribution lives in `NOTICE` and, for the embedded table, in the
`retrieved_at` and `attribution` fields of `internal/pricing/embedded.json`.
