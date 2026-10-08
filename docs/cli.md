# CLI

`ai-usage-exporter` serves metrics when invoked without a command. `serve` is
the explicit equivalent. The default listener is `127.0.0.1:9477`.

`ai-usage-exporter providers --output json` lists registered data sources.
`ai-usage-exporter report --window 30d --output json` scans once, prints token
rows and estimated cost, then exits. `cost_usd: null` means the model has no
catalog entry, while zero means its catalog price is explicitly zero.

`ai-usage-exporter doctor --output json` scans once and prints discovery and
parse health per provider, including skipped files, budget exhaustion, and
models with explicit zero pricing separately from unpriced models.
Each doctor row includes the platform-resolved `roots`, a `skipped` total with
`skipped_by_size`, `skipped_by_type`, and `skipped_by_budget` breakdowns, and
the first parsing error when one occurred. Warnings stay on stderr, so stdout
remains valid JSON for scripts even when a provider had parse failures.

Supported windows are `24h`, `7d`, `30d`, `mtd`, and `all`. JSON always goes
to stdout; diagnostics go to stderr. Exit codes are 0 for success, 1 for an
execution failure, and 2 for invalid command usage.

## Example output

The following is abbreviated output from one local scan:

```text
$ ai-usage-exporter version
ai-usage-exporter dev (none, built unknown, go1.25)
$ ai-usage-exporter providers
claude-code     Claude Code     jsonl
codex           OpenAI Codex    jsonl
opencode        OpenCode        sqlite
$ ai-usage-exporter report --window 30d
claude-code     claude-opus-5   input       3950    0.007900
opencode        big-pickle      cache_read  17133184 0.000000
$ ai-usage-exporter doctor
claude-code     available=true  files=39    skipped=0 errors=0 budget_hit=false
codex           available=true  files=276   skipped=0 errors=0 budget_hit=false
opencode        available=true  files=1     skipped=0 errors=0 budget_hit=false
```
