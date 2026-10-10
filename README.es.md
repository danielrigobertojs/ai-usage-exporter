[Read in English](README.md)

## ai-usage-exporter

Exporter de Prometheus en Go para el uso local de agentes de IA: tokens,
modelos, sesiones y coste expuestos en `/metrics`.

### Estado del proyecto

El binario escanea los proveedores registrados y sirve `/metrics`.

### Ejecutar el exporter

```bash
make build
./ai-usage-exporter
curl http://127.0.0.1:9477/metrics
```

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
make build
make cross
make test
make vet
make fmt
```

### Plataformas soportadas

| SO | Arquitecturas |
|---|---|
| darwin | amd64, arm64 |
| linux | amd64, arm64 |
| windows | amd64, arm64 |

### Installation

Los archivos de release, paquetes Linux, Docker, instalación con Go y la
configuración opcional de servicios systemd/launchd se documentan en
[docs/install.md](docs/install.md).

### Licencia

[Apache License 2.0](LICENSE). Ver [NOTICE](NOTICE),
[THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md), [TRADEMARK.md](TRADEMARK.md),
[CONTRIBUTING.md](CONTRIBUTING.md) y [CITATION.cff](CITATION.cff).
