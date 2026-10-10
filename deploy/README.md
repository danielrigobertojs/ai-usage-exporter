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
Exporter — Overview" dashboard already loaded under Home → Dashboards. No
manual import is required: `deploy/grafana/provisioning/dashboards/ai-usage-exporter.yaml`
tells Grafana to read it from `deploy/grafana/dashboards/`.

Step 2 intentionally fails if `deploy/.env` does not exist
(`GRAFANA_ADMIN_USER` and `GRAFANA_ADMIN_PASSWORD` have no default value in
`docker-compose.yml`): this repository contains no hard-coded credentials or
implicit `admin/admin` account. `deploy/.env` is listed in `.gitignore`; do
not commit it.

### If the exporter does not run on the host

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

### Import only the dashboard into an existing Grafana installation

If you already have Grafana running and only want the dashboard, without this
`docker-compose.yml`:

1. Ensure that a Prometheus data source scrapes `ai-usage-exporter`.
2. Go to Dashboards → New → Import and upload
   `deploy/grafana/dashboards/ai-usage-overview.json`.
3. Grafana prompts you to choose the data source for the `$datasource`
   variable (it is a `datasource` variable, not a fixed UID, which is why the
   same JSON supports both file provisioning and manual import).

### Why `scrape_interval: 15s`

`ai-usage-exporter` rescans the complete logs every 60 seconds by default
(see [ADR-001](../docs/adr/0001-startup-scan-and-gauges.md)). Scraping every
15 seconds makes the new snapshot and its freshness visible without waiting
for another Prometheus minute. Usage metrics remain windowed gauges; they do
not become counters.

### What not to do with usage series

Usage metrics (`ai_usage_tokens`, `ai_usage_cost_usd`,
`ai_usage_sessions`, `ai_usage_tool_calls`) are **windowed aggregate gauges**,
not counters: never apply `rate()` or `increase()` to them. The dashboard's
"Token trend" panel repeats this in its description.
