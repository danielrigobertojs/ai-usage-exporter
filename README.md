[Leer en español](README.es.md)

## ai-usage-exporter

Go Prometheus exporter for local AI-agent usage (Claude Code, Codex CLI,
OpenCode, and others): tokens, models, sessions, and cost read directly from
each tool's local logs and exposed through a
endpoint `/metrics`.

### Project status

The binary scans registered providers and serves `/metrics`. It includes:

- Go module layout (`cmd/ai-usage-exporter`, `internal/...`).
- [ADR-001](docs/adr/0001-startup-scan-and-gauges.md) for scans and gauges,
  and [ADR-002](docs/adr/0002-stack-and-build-constraints.md) for CGO-free builds.
- The [metrics contract](docs/metrics.md), parallel provider scanning, the
  Prometheus collector, and the HTTP endpoints `/metrics`, `/healthz`, and `/readyz`.
- Claude Code, Codex, and OpenCode providers.

### Run the exporter

```bash
make build
./ai-usage-exporter
curl http://127.0.0.1:9477/metrics
```

By default, it listens on **`127.0.0.1:9477`**, never `0.0.0.0`. Local logs
may contain prompts near usage metadata, so network exposure must be explicit:
use `--listen`, `AI_USAGE_LISTEN`, or `listen` in the configuration file.

Configuration precedence is: built-in defaults → YAML file
(`<xdg_config>/ai-usage-exporter/config.yaml`,
`%APPDATA%\ai-usage-exporter\config.yaml` on Windows) → environment variables
`AI_USAGE_*` → flags (`--listen`, `--metrics-path`, `--scan-interval`,
`--scan-timeout`, `--timezone`, `--providers`, `--labels-project`). See
`internal/config`. Structured logging is documented in [docs/logging.md](docs/logging.md).
Use `AI_USAGE_FAIL_ON_STARTUP_SCAN_ERROR=true` or `--fail-on-startup-scan-error`
to make an initial timeout fatal.

`/metrics` serves complete log snapshots
([ADR-001](docs/adr/0001-startup-scan-and-gauges.md)), refreshed every 60
seconds by default. `AI_USAGE_SCAN_INTERVAL=0` retains a frozen snapshot;
`SIGHUP` can also request a rescan.

### Requirements

- Go 1.25 or later.
- No C toolchain: the project builds with `CGO_ENABLED=0` on every platform.

### Build

```bash
make build   # current-platform binary at ./ai-usage-exporter
make cross   # darwin/linux/windows × amd64/arm64 at ./dist
make test    # go test ./... -race
make vet     # go vet ./...
make fmt     # gofmt -l .
```

### Supported platforms

| OS | Architectures |
|---|---|
| darwin | amd64, arm64 |
| linux | amd64, arm64 |
| windows | amd64, arm64 |

### Installation

Release archives, Linux packages, Docker, Go installation, and optional
systemd/launchd service setup are documented in [docs/install.md](docs/install.md).

### License

[Apache License 2.0](LICENSE). Third-party notices are in [NOTICE](NOTICE) and
[THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md). See [TRADEMARK.md](TRADEMARK.md),
[CONTRIBUTING.md](CONTRIBUTING.md) (DCO required), and [CITATION.cff](CITATION.cff).
