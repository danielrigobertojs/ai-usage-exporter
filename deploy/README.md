## Example stack: Prometheus + Grafana

This directory starts Prometheus and Grafana with the `ai-usage-exporter`
dashboard already provisioned. It does **not** start the exporter: the exporter
runs on the host, outside Docker, because it reads the user's local logs
(`~/.claude`, `~/.codex`, `~/.local/share/opencode`), which should not be
mounted into a container.

### Start in three commands

```bash
./ai-usage-exporter --listen 127.0.0.1:9477      # 1. the exporter, on the host
cp deploy/.env.example deploy/.env               # 2. Grafana credentials (edit before continuing)
docker compose -f deploy/docker-compose.yml up -d  # 3. Prometheus + Grafana
```

Grafana is available at **http://localhost:3000**, with the "AI Usage
Exporter — Overview" and "AI Usage — Live por instancia, provider y modelo"
dashboards already loaded under Home → Dashboards. No manual import is
required: `deploy/grafana/provisioning/dashboards/ai-usage-exporter.yaml` tells
Grafana to read them from `deploy/grafana/dashboards/`.

Step 2 intentionally fails if `deploy/.env` does not exist
(`GRAFANA_ADMIN_USER` and `GRAFANA_ADMIN_PASSWORD` have no default value in
`docker-compose.yml`): this repository contains no hard-coded credentials or
implicit `admin/admin` account. `deploy/.env` is listed in `.gitignore`; do
not commit it.

### If the exporter does not run on the host

If an existing host needs a newer exporter binary, follow the [upgrade an
existing installation](../docs/install.md#upgrade-an-existing-installation)
procedure first. It covers both managed services and manually started
processes, plus verification of the running build.

`deploy/prometheus/prometheus.yml` targets `host.docker.internal:9477`, which
Docker resolves to the host IP. This works in Docker Desktop (macOS/Windows)
and on Linux through the `extra_hosts: host-gateway` entry already present on
the `prometheus` service in `docker-compose.yml`.

If the exporter runs on another machine or port, change the single target in
`deploy/prometheus/prometheus.yml`:

```yaml
scrape_configs:
  - job_name: ai-usage-exporter
    static_configs:
      - targets: ["another-machine:9477"]
```

Then restart Prometheus (`docker compose -f deploy/docker-compose.yml restart prometheus`).

### Scrape multiple exporter hosts

One Prometheus can scrape several exporter machines by listing each target in
the same `static_configs` block (or by using service discovery). Prometheus
adds the `instance` and `job` labels at scrape time; the exporter itself still
emits only `tool`, `model`, `token_type`, and `window`.

```yaml
scrape_configs:
  - job_name: ai-usage-exporter
    static_configs:
      - targets: ["workstation-a:9477", "workstation-b:9477"]
```

Use **AI Usage — Live por instancia, provider y modelo** to filter and compare
individual hosts. **AI Usage Exporter — Overview** intentionally aggregates the
fleet, so its totals are not per-host figures.

### Import only the dashboard into an existing Grafana installation

If you already have Grafana running and only want the dashboard, without this
`docker-compose.yml`:

1. Ensure that a Prometheus data source scrapes `ai-usage-exporter`.
2. Go to Dashboards → New → Import and upload
   `deploy/grafana/dashboards/ai-usage-overview.json` or
   `deploy/grafana/dashboards/ai-usage-live.json`.
3. Grafana prompts you to choose the data source for the `$datasource`
   variable (it is a `datasource` variable, not a fixed UID, which is why the
   same JSON supports both file provisioning and manual import).

### Why `scrape_interval: 15s`

`ai-usage-exporter` rescans the complete logs every 60 seconds by default
(see [ADR-001](../docs/adr/0001-startup-scan-and-gauges.md)). Scraping every
15 seconds makes the new snapshot and its freshness visible without waiting
for another Prometheus minute. Usage metrics remain windowed gauges; they do
not become counters.

### Alert rules

`deploy/prometheus/ai-usage-exporter.rules.yml` is loaded by the example
Prometheus configuration. Its availability alert selects
`job="ai-usage-exporter"`, which must match the `job_name` in the scrape
configuration above. If an existing Prometheus uses another job name, set that
selector to its exporter job name when copying the rules. The remaining rules
select exporter metrics directly and do not depend on a job label.
The shipped selector matches the example configuration only; change it before
copying these rules into an existing Prometheus configuration.

The rules use a five-minute `for:` for an unavailable exporter, failed scan,
and stale scan timestamp: this tolerates short scrape/network interruptions
while reporting a sustained outage promptly. Provider-source and no-events
alerts use ten minutes so a transient scan problem cannot page before the next
60-second scan has had several chances to recover.

`AIUsageProviderUnavailable` only alerts for a provider that was available in
the preceding 24 hours, so a tool that is not installed on a host does not
produce a permanent alert. `AIUsageScanStale` uses a 300-second threshold for
the default 60-second scan interval; increase that threshold to exceed any
larger `AI_USAGE_SCAN_INTERVAL` before deploying it.

To run the optional Prometheus rule-evaluation fixtures locally (without adding
a Go dependency), use the Prometheus image:

```bash
docker run --rm --entrypoint /bin/promtool \
  -v "$PWD/deploy/prometheus:/etc/prometheus:ro" -w /etc/prometheus \
  prom/prometheus:v3.0.1 test rules testdata/ai-usage-exporter.rules.test.yml
```

### What not to do with usage series

Usage metrics (`ai_usage_tokens`, `ai_usage_cost_usd`,
`ai_usage_sessions`, `ai_usage_tool_calls`) are **windowed aggregate gauges**,
not counters: never apply `rate()` or `increase()` to them. The dashboard's
"Token trend" panel repeats this in its description.
