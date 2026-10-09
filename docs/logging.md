<!-- SPDX-License-Identifier: Apache-2.0 -->

## Logging

The exporter writes structured operational logs to stderr. Set
`AI_USAGE_LOG_LEVEL` to `debug`, `info` (the default), `warn`, or `error`.
Set `AI_USAGE_LOG_FORMAT` to `text` (the default) or `json` for log collectors
such as Loki. The matching command-line flags are `--log-level` and
`--log-format`.

Every line produced while scanning carries the same short `scan_id`. Source
paths are deliberately emitted only at `debug`; prompts, tool output, and raw
JSONL content are never logged.

| Event | Level | Attributes |
| --- | --- | --- |
| Configuration, provider registration, pricing catalog | INFO | resolved safe settings and catalog source |
| Discovery and snapshot publication | INFO | provider totals, duration, aggregate token data |
| Individual source read and scrape | DEBUG | source index, path, size, duration |
| Skipped input, exhausted budget, parse degradation | INFO/WARN | aggregate reason or safe error type |
| Discovery or rescan failure | ERROR | safe error type |

This is output from the log tests' real scan fixture (the timestamp varies):

```text
time=2026-10-09T00:00:00.000Z level=INFO msg="scan started" scan_id=scan-test providers=1
time=2026-10-09T00:00:00.000Z level=INFO msg="provider discovery complete" scan_id=scan-test tool=alpha files=1 skipped=0
time=2026-10-09T00:00:00.000Z level=INFO msg="scan complete" scan_id=scan-test duration=0s tools=1 events_duplicates=0 events_invalid=0
```

The equivalent JSON mode is one valid JSON object per line:

```json
{"time":"2026-10-09T00:00:00Z","level":"INFO","msg":"scan started","scan_id":"scan-test","providers":1}
```
