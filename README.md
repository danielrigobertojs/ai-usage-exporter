## ai-usage-exporter

Exporter de Prometheus en Go para el uso local de agentes de IA (Claude
Code, Codex CLI, OpenCode y otros): tokens, modelo, sesiones y coste, leídos
directamente de los logs locales de cada herramienta y expuestos en un
endpoint `/metrics`.

### Estado del proyecto

Este repositorio está en bootstrap. Todavía no hay providers, métricas
reales ni servidor HTTP — eso llega en los issues siguientes. Lo que existe
hoy:

- Layout del módulo Go (`cmd/ai-usage-exporter`, `internal/version`).
- Las dos decisiones de arquitectura que condicionan todo lo demás:
  [ADR-001](docs/adr/0001-startup-scan-and-gauges.md) (parseo al arranque y
  gauges en lugar de counters) y
  [ADR-002](docs/adr/0002-stack-and-build-constraints.md) (stack sin CGO).
- El [contrato de métricas](docs/metrics.md) que fija nombres, labels y
  presupuesto de cardinalidad para todo el proyecto.

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

### Licencia

[Apache License 2.0](LICENSE). Avisos de atribución de terceros en
[NOTICE](NOTICE) y [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md).
Política de marca en [TRADEMARK.md](TRADEMARK.md). Para contribuir, ver
[CONTRIBUTING.md](CONTRIBUTING.md) (requiere DCO). Para citar este
proyecto, ver [CITATION.cff](CITATION.cff).
