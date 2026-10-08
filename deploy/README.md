## Stack de ejemplo: Prometheus + Grafana

Este directorio levanta Prometheus y Grafana con el dashboard de
`ai-usage-exporter` ya provisionado. **No** levanta el exporter: eso corre en
el host, fuera de Docker, porque lee logs locales del usuario
(`~/.claude`, `~/.codex`, `~/.local/share/opencode`) y no tiene sentido
montarlos dentro de un contenedor.

### Arrancar en 3 comandos

```bash
./ai-usage-exporter --listen 127.0.0.1:9477      # 1. el exporter, en el host
cp deploy/.env.example deploy/.env               # 2. credenciales de Grafana (editar antes de seguir)
docker compose -f deploy/docker-compose.yml up -d  # 3. Prometheus + Grafana
```

Grafana queda en **http://localhost:3000**, con el dashboard "AI Usage
Exporter — Overview" ya cargado en Home → Dashboards. No hace falta importar
nada a mano: `deploy/grafana/provisioning/dashboards/ai-usage-exporter.yaml`
le dice a Grafana que lo lea de `deploy/grafana/dashboards/`.

El paso 2 falla a propósito si `deploy/.env` no existe (`GRAFANA_ADMIN_USER`
/ `GRAFANA_ADMIN_PASSWORD` no tienen valor por defecto en
`docker-compose.yml`): no hay ninguna credencial hardcodeada en este repo,
ni un `admin/admin` implícito. `deploy/.env` está en `.gitignore`; no lo
commitees.

### Si el exporter no corre en el host

`deploy/prometheus/prometheus.yml` apunta a `host.docker.internal:9477`,
que Docker resuelve a la IP del host — funciona así en Docker Desktop
(macOS/Windows) y, en Linux, gracias al `extra_hosts: host-gateway` que ya
tiene el servicio `prometheus` en `docker-compose.yml`.

Si el exporter corre en otra máquina, o en otro puerto, cambia el único
target en `deploy/prometheus/prometheus.yml`:

```yaml
scrape_configs:
  - job_name: ai-usage-exporter
    static_configs:
      - targets: ["otra-maquina:9477"]
```

y reinicia Prometheus (`docker compose -f deploy/docker-compose.yml restart prometheus`).

### Importar solo el dashboard en una Grafana existente

Si ya tienes Grafana corriendo y solo quieres el dashboard, sin este
`docker-compose.yml`:

1. Asegúrate de tener un datasource de Prometheus apuntando a donde sea que
   scrapees `ai-usage-exporter`.
2. Dashboards → New → Import, y sube
   `deploy/grafana/dashboards/ai-usage-overview.json`.
3. Grafana te pedirá elegir el datasource para la variable `$datasource`
   (es una variable de tipo "datasource", no un UID fijo — por eso el mismo
   JSON sirve igual para provisioning por archivo que para import manual).

### Por qué `scrape_interval: 15s`

`ai-usage-exporter` reescanea los logs completos cada 60 segundos por
defecto (ver [ADR-001](../docs/adr/0001-startup-scan-and-gauges.md)).
Scrapear cada 15 segundos hace visibles el snapshot nuevo y su frescura sin
esperar otro minuto de Prometheus. Las métricas de uso siguen siendo gauges
por ventana y no se convierten en counters.

### Qué no mirar en las series de uso

Las métricas de uso (`ai_usage_tokens`, `ai_usage_cost_usd`,
`ai_usage_sessions`, `ai_usage_tool_calls`) son **gauges agregados por
ventana**, no counters: nunca les apliques `rate()` ni `increase()`. El
panel "Tendencia de tokens" del dashboard lo recuerda en su descripción.
