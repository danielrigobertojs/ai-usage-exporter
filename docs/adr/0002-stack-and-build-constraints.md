# ADR-002: Stack y restricciones de compilación

- Fecha: 2026-10-02
- Estado: Aceptado
- Revisión: reconsiderar si surge un requisito que solo un driver CGO de
  SQLite pueda cumplir (ver "Condición de reapertura")

## Contexto

El entregable de `ai-usage-exporter` es un binario único que un operador
descarga y ejecuta directamente — sin runtime, sin contenedor obligatorio,
sin pasos de instalación adicionales — en darwin, linux y windows, cada uno
en amd64 y arm64 (6 combinaciones). Uno de los providers planeados (OpenCode)
lee una base SQLite (`~/.local/share/opencode/opencode.db`).

El driver de SQLite más conocido en Go, `mattn/go-sqlite3`, usa CGO: enlaza
la librería C de SQLite. Eso exige un toolchain de C disponible (y
configurado para la arquitectura destino) en cada entorno donde se compile,
lo que rompe el cross-compilation de un solo `go build` por plataforma y
complica cualquier pipeline de CI que no tenga ese toolchain preinstalado
para las 6 combinaciones de destino.

## Decisión

- **Go 1.25 o superior** como versión mínima del toolchain (`go.mod` fija
  `go 1.25`).
- **`CGO_ENABLED=0` es obligatorio**, no una preferencia de estilo. Se aplica
  explícitamente en `make build` y `make cross`, y se verifica en CI.
- Como consecuencia directa, el driver de SQLite para el provider de
  OpenCode debe ser **`modernc.org/sqlite`** (implementación pura en Go,
  transpilada desde SQLite en C). **Nunca** `mattn/go-sqlite3` ni ningún otro
  driver que requiera CGO.
- Dependencias de runtime permitidas hoy, sin ampliar este ADR:
  - `github.com/prometheus/client_golang` — cliente oficial de métricas de
    Prometheus; es el estándar de facto y evita reimplementar el formato de
    exposición y el servidor de scraping.
  - `modernc.org/sqlite` — único driver SQLite puro en Go con soporte
    maduro de modo solo-lectura y WAL, necesario para el provider de
    OpenCode sin romper la restricción de CGO.
  - `github.com/spf13/cobra` — estructura de subcomandos de la CLI
    (`serve`, `version`, etc.) cuando el binario los necesite; evita
    reimplementar parsing de flags y ayuda por subcomando a mano.

  Cualquier dependencia de runtime fuera de esta lista se justifica
  ampliando este ADR en el issue correspondiente antes de añadirla al
  `go.mod`.

## Alternativas descartadas

**`mattn/go-sqlite3` (o cualquier driver basado en CGO).** Es más maduro y
previsiblemente más rápido que un driver puro en Go, pero exige
`CGO_ENABLED=1` y un toolchain de C por plataforma destino. Eso contradice
directamente el requisito de un binario único cross-compilado sin
dependencias externas en tiempo de build, y complica CI (imágenes con
toolchains de C para 6 combinaciones OS/arch en lugar de solo el toolchain de
Go). Se descarta mientras el binario único sin CGO sea un requisito del
producto.

## Consecuencias

- `make cross` debe fallar el build si cualquier paquete en la ruta de
  compilación requiere CGO; en la práctica esto se verifica compilando con
  `CGO_ENABLED=0` explícito para las 6 combinaciones de `GOOS`/`GOARCH`.
- El provider de OpenCode paga el costo de rendimiento y madurez relativo de
  `modernc.org/sqlite` frente a un driver CGO; se acepta porque el volumen de
  datos esperado (logs locales de un único usuario) no lo hace un cuello de
  botella.
- Cualquier dependencia nueva de runtime (no solo de test) requiere
  justificación escrita en el issue que la introduce antes de hacer merge.

## Condición de reapertura

Reabrir esta decisión si aparece un requisito que solo un driver CGO de
SQLite pueda satisfacer (por ejemplo, una limitación de rendimiento o
compatibilidad de `modernc.org/sqlite` que bloquee el provider de OpenCode) y
el equipo decide aceptar el costo de requerir un toolchain de C en el pipeline
de build para ese caso.
