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

Supported windows are `24h`, `7d`, `30d`, `mtd`, and `all`. JSON always goes
to stdout; diagnostics go to stderr. Exit codes are 0 for success, 1 for an
execution failure, and 2 for invalid command usage.
