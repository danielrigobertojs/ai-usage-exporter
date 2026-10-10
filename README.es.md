[Read in English](README.md)

## ai-usage-exporter

Exporter de Prometheus en Go para el uso local de agentes de IA (Claude Code,
Codex CLI, OpenCode y otros): tokens, modelos, sesiones y coste leídos
directamente de los logs locales de cada herramienta y expuestos en un endpoint
HTTP `/metrics`.

### Estado del proyecto

El binario escanea los proveedores registrados y sirve `/metrics`. Incluye:

- El layout del módulo Go (`cmd/ai-usage-exporter`, `internal/...`).
- El [ADR-001](docs/adr/0001-startup-scan-and-gauges.md) sobre escaneos y
  gauges, y el [ADR-002](docs/adr/0002-stack-and-build-constraints.md) para
  builds sin CGO.
- El [contrato de métricas](docs/metrics.md), escaneo paralelo de providers, el
  collector de Prometheus y los endpoints HTTP `/metrics`, `/healthz` y `/readyz`.
- Los providers de Claude Code, Codex y OpenCode.

### Ejecutar el exporter

```bash
make build
./ai-usage-exporter
curl http://127.0.0.1:9477/metrics
```

Por defecto escucha en **`127.0.0.1:9477`**, nunca en `0.0.0.0`. Los logs
locales pueden contener prompts cerca de los metadatos de uso, por lo que la
exposición de red debe ser explícita: usa `--listen`, `AI_USAGE_LISTEN` o
`listen` en el archivo de configuración.

La precedencia de configuración es: defaults integrados → archivo YAML
(`<xdg_config>/ai-usage-exporter/config.yaml`,
`%APPDATA%\\ai-usage-exporter\\config.yaml` en Windows) → variables de entorno
`AI_USAGE_*` → flags (`--listen`, `--metrics-path`, `--scan-interval`,
`--scan-timeout`, `--timezone`, `--providers`, `--labels-project`). Consulta
`internal/config`. La documentación de logging estructurado está en
[docs/logging.md](docs/logging.md). Usa `AI_USAGE_FAIL_ON_STARTUP_SCAN_ERROR=true`
o `--fail-on-startup-scan-error` para hacer fatal un timeout inicial.

`/metrics` sirve snapshots completos de los logs
([ADR-001](docs/adr/0001-startup-scan-and-gauges.md)), actualizados cada 60
segundos por defecto. `AI_USAGE_SCAN_INTERVAL=0` conserva un snapshot
congelado; `SIGHUP` también puede solicitar un reescaneo.

### Cambio en la estimación de coste de JCB-327

Las versiones anteriores a `bfdcfbf` subestimaban el coste porque los tokens
de caché se valoraban a cero. Al actualizar, el coste de 30 días puede subir
aproximadamente 4.4×; es la corrección, no una regresión. Ver
[ADR-006](docs/adr/0006-models-dev-provider-precedence.md).

### Requisitos

- Go 1.25 o posterior.
- `CGO_ENABLED=0` en todas las plataformas soportadas.

### Build

```bash
make build   # binario de la plataforma actual en ./ai-usage-exporter
make cross   # darwin/linux/windows × amd64/arm64 en ./dist
make test    # go test ./... -race
make vet     # go vet ./...
make fmt     # gofmt -l .
```

### Plataformas soportadas

| SO | Arquitecturas |
|---|---|
| darwin | amd64, arm64 |
| linux | amd64, arm64 |
| windows | amd64, arm64 |

### Instalación

Los archivos de release, paquetes Linux, Docker, instalación con Go y la
configuración opcional de servicios systemd/launchd se documentan en
[docs/install.md](docs/install.md).

### Licencia

[Apache License 2.0](LICENSE). Los avisos de terceros están en [NOTICE](NOTICE)
y [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md). Consulta
[TRADEMARK.md](TRADEMARK.md), [CONTRIBUTING.md](CONTRIBUTING.md) (DCO obligatorio)
y [CITATION.cff](CITATION.cff).
