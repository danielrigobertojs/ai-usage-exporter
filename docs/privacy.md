<!-- SPDX-License-Identifier: Apache-2.0 -->

# Privacy

The exporter reads local agent history once at startup (and after an operator-triggered rescan) to derive aggregate token, model, session, tool-call, and estimated-cost metrics. It does not send agent logs to a remote service.

## Data read

The parsers use a closed schema. Fields not listed below are not decoded.

| Format | Fields extracted |
| --- | --- |
| Claude Code JSONL | entry `type`, `uuid`, `sessionId`, `cwd`, `timestamp`; message `model`, usage `input_tokens`, `output_tokens`, `cache_read_input_tokens`, `cache_creation_input_tokens`; content block `type` only |
| Codex JSONL | line `type`, `timestamp`; session `id`, `session_id`, `model`; turn `model`; event `type`; token totals `input_tokens`, `cached_input_tokens`, `output_tokens`, `reasoning_output_tokens`, `total_tokens`; response item `type` only |
| OpenCode SQLite | message `id`, `session_id`, JSON `modelID`, `providerID`, `time.created`, token `total`, `input`, `output`, cache `read`/`write`, `reasoning`, and `cost`; session `directory` |

Prompt text, assistant text, tool arguments, tool results, command output, summaries, and arbitrary JSON fields are not decoded or exported.

## Output and logs

`/metrics` exposes aggregate gauges with tool, model, token type, and time-window labels. It never includes session IDs, log content, or filesystem paths. It is served with `Cache-Control: no-store` and binds to `127.0.0.1` by default. Do not expose this endpoint beyond localhost without a separate access-control and TLS design.

Errors use a provider-relative filename and a safe error category; they do not render a source line or an absolute path. Debug logs may identify an indexed source but do not include content. The `doctor` command is intentionally different: it prints resolved roots only when the operator explicitly requests diagnostics, so treat its output as local operational information and redact it before sharing.

