## ai-usage-exporter

Exporter de Prometheus en Go para el uso local de agentes de IA (Claude
Code, Codex CLI, OpenCode y otros): tokens, modelo, sesiones y coste, leídos
directamente de los logs locales de cada herramienta y expuestos en un
endpoint `/metrics`.

### Estado del proyecto

El binario ya escanea los providers registrados y sirve `/metrics`. Lo que
existe hoy:

- Layout del módulo Go (`cmd/ai-usage-exporter`, `internal/...`).
- Las dos decisiones de arquitectura que condicionan todo lo demás:
  [ADR-001](docs/adr/0001-startup-scan-and-gauges.md) (parseo al arranque y
  gauges en lugar de counters) y
  [ADR-002](docs/adr/0002-stack-and-build-constraints.md) (stack sin CGO).
- El [contrato de métricas](docs/metrics.md) que fija nombres, labels y
  presupuesto de cardinalidad para todo el proyecto.
- El escaneo paralelo de providers (`internal/scan`), el collector de
  Prometheus (`internal/collector`) y el servidor HTTP (`internal/server`)
  con `/metrics`, `/healthz` y `/readyz`.
- Un único provider registrado por ahora: Claude Code
  (`internal/provider/claudecode`). Codex y OpenCode llegan en JCB-315.

### Ejecutar el exporter

```bash
make build
./ai-usage-exporter
curl http://127.0.0.1:9477/metrics
```

Por defecto escucha en **`127.0.0.1:9477`**, nunca en `0.0.0.0`: el binario
lee logs locales que pueden contener prompts cerca de los metadatos de uso,
y exponerlo a la red por defecto sería un fallo de diseño, no una
conveniencia. Cambiarlo es explícito, vía `--listen`, `AI_USAGE_LISTEN` o
`listen` en el archivo de config.

Configuración, en orden de precedencia creciente: defaults incorporados →
archivo YAML (`<xdg_config>/ai-usage-exporter/config.yaml`,
`%APPDATA%\ai-usage-exporter\config.yaml` en Windows) → variables
`AI_USAGE_*` → flags (`--listen`, `--metrics-path`, `--scan-interval`,
`--scan-timeout`, `--timezone`, `--providers`, `--labels-project`). Ver
`internal/config`.

`/metrics` se sirve a partir de un único escaneo hecho al arrancar
([ADR-001](docs/adr/0001-startup-scan-and-gauges.md)): los valores no
avanzan mientras el proceso vive, salvo que se active explícitamente un
reescaneo con `SIGHUP` o `scan_interval` (ambos apagados por defecto — ver
la sección "Excepción explícita y opt-in" del ADR-001).

### Requisitos

- Go 1.25 o superior.
- Sin toolchain de C: el proyecto compila con `CGO_ENABLED=0` en todas las
  plataformas (ver ADR-002).

### Build

```bash
make build   # binario para la plataforma actual, en ./ai-usage-exporter
make cross   # darwin/linux/windows × amd64/arm64 en ./dist
make test    # go test ./... -race
make vet     # go vet ./...
make fmt     # gofmt -l .
```

### Plataformas soportadas

| OS | Arquitecturas |
|---|---|
| darwin | amd64, arm64 |
| linux | amd64, arm64 |
| windows | amd64, arm64 |

### Installation

Release archives, Linux packages, Docker, Go installation, and optional
systemd/launchd service setup are documented in [docs/install.md](docs/install.md).

### Licencia

[Apache License 2.0](LICENSE). Avisos de atribución de terceros en
[NOTICE](NOTICE) y [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md).
Política de marca en [TRADEMARK.md](TRADEMARK.md). Para contribuir, ver
[CONTRIBUTING.md](CONTRIBUTING.md) (requiere DCO). Para citar este
proyecto, ver [CITATION.cff](CITATION.cff).
