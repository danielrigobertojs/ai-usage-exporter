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
`job=~"ai-usage-exporter|ai-worker"`, covering the example `job_name` and the
worker job used by the reference deployment. If an existing Prometheus uses a
different job name, add it to this selector before copying the rules. The
remaining rules select exporter metrics directly and do not depend on a job
label.

The rules use a five-minute `for:` for an unavailable exporter, failed scan,
and stale scan timestamp: this tolerates short scrape/network interruptions
while reporting a sustained outage promptly. Provider-source and no-events
alerts use ten minutes so a transient scan problem cannot page before the next
60-second scan has had several chances to recover.

`AIUsageProviderUnavailable` only alerts for a provider that was available in
the preceding 24 hours, so a tool that is not installed on a host does not
produce a permanent alert. A provider that has never been available is
indistinguishable from an uninstalled tool in this metric; inspect
`ai_usage_scan_errors` when diagnosing that state. The same 24-hour guard
means a continuously unavailable provider stops matching this alert after its
last available sample ages out; the exporter cannot distinguish that state from
an uninstalled tool without another metric. `AIUsageScanStale` uses a 300-second
threshold for the default 60-second scan interval; increase that threshold to
exceed any larger `AI_USAGE_SCAN_INTERVAL` before deploying it.

### Install alert rules in an existing Prometheus

To add these rules to a Prometheus that is already running, copy
`deploy/prometheus/ai-usage-exporter.rules.yml` to its configuration directory
on the host, for example `<config-dir>/ai-usage-exporter.rules.yml`. Keep the
rule file alongside the Prometheus configuration so it can be validated and
deployed as one configuration change.

The `rule_files` path is evaluated by the Prometheus process, not by the host.
Add the path visible to that process to the host's `prometheus.yml` (or
`prometheus.yaml`):

```yaml
rule_files:
  - /etc/prometheus/ai-usage-exporter.rules.yml
```

For Prometheus installed from a package or run directly as a binary, choose the
installed configuration directory and use the corresponding local path instead
(for example, `<config-dir>/ai-usage-exporter.rules.yml`). Ensure that the
Prometheus service account can read both files.

For Docker Compose, copying the rule file onto the host is not enough: it must
also be available inside the container. Either add a second bind mount, matching
the example stack in this repository:

```yaml
services:
  prometheus:
    volumes:
      - ./config/prometheus.yaml:/etc/prometheus/prometheus.yml:ro
      - ./config/ai-usage-exporter.rules.yml:/etc/prometheus/ai-usage-exporter.rules.yml:ro
```

or mount the entire configuration directory, provided the configuration uses
the paths inside that directory. The second bind is useful when the existing
Compose setup mounts only `prometheus.yaml`; mounting the directory is useful
when Prometheus should read several related configuration files.

Before restarting or recreating Prometheus, validate both the configuration and
the rules with `promtool`. For a package or binary installation, where the
configured absolute paths exist on the host, run:

```bash
promtool check config <config-dir>/prometheus.yaml
promtool check rules <config-dir>/ai-usage-exporter.rules.yml
```

Use the actual filename if the existing installation calls its main file
`prometheus.yml`. For Docker Compose, an absolute `rule_files` path such as
`/etc/prometheus/ai-usage-exporter.rules.yml` exists only in the container, so
a host-side `promtool check config` cannot validate it. Run `promtool` in a
temporary container with the same read-only mounts as the Prometheus service:

```bash
docker run --rm --entrypoint /bin/promtool \
  -v ./config/prometheus.yaml:/etc/prometheus/prometheus.yml:ro \
  -v ./config/ai-usage-exporter.rules.yml:/etc/prometheus/ai-usage-exporter.rules.yml:ro \
  prom/prometheus:latest check config /etc/prometheus/prometheus.yml

docker run --rm --entrypoint /bin/promtool \
  -v ./config/prometheus.yaml:/etc/prometheus/prometheus.yml:ro \
  -v ./config/ai-usage-exporter.rules.yml:/etc/prometheus/ai-usage-exporter.rules.yml:ro \
  prom/prometheus:latest check rules /etc/prometheus/ai-usage-exporter.rules.yml
```

Replace `./config` and the container-side filenames with the paths used by the
existing Compose service. A `rule_files` entry that names a path not mounted
into a container is a startup failure, not a warning: Prometheus will not
start. These checks validate the exact files and paths that the replacement
container will see before it is recreated.

If Prometheus was started with `--web.enable-lifecycle`, it can reload the
updated configuration without a container restart. Otherwise there is no hot
reload endpoint, so recreate the Compose service (or restart the service that
runs the binary/package installation) after validation.

To run the optional Prometheus rule-evaluation fixtures locally (without adding
a Go dependency), use the Prometheus image:

```bash
docker run --rm --entrypoint /bin/promtool \
  -v "$PWD/deploy/prometheus:/etc/prometheus:ro" -w /etc/prometheus \
  prom/prometheus:v3.0.1 test rules testdata/ai-usage-exporter.rules.test.yml
```

A successful run reports `SUCCESS`; CI runs this same fixture on Linux.

### What not to do with usage series

Usage metrics (`ai_usage_tokens`, `ai_usage_cost_usd`,
`ai_usage_sessions`, `ai_usage_tool_calls`) are **windowed aggregate gauges**,
not counters: never apply `rate()` or `increase()` to them. The dashboard's
"Token trend" panel repeats this in its description.
