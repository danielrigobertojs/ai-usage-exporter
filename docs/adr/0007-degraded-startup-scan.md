<!-- SPDX-License-Identifier: Apache-2.0 -->

## ADR-007: arranque degradado cuando falla el escaneo inicial

Fecha: 2026-10-09. Revisión: 2027-01-09.

El exporter no termina cuando el escaneo de inicio vence su timeout o falla.
Sirve `/metrics` con las métricas meta y `ai_usage_scan_success 0`, pero no
publica métricas de uso de un agregado parcial. El siguiente reescaneo puede
recuperar normalmente. `--fail-on-startup-scan-error` y
`AI_USAGE_FAIL_ON_STARTUP_SCAN_ERROR=true` restauran el modo estricto.

Se descartó fallar siempre porque Prometheus no puede distinguirlo de un host
caído. También se descartó publicar el resultado parcial: los gauges podrían
retroceder y producir una lectura falsa. Esta decisión se revisará en la fecha
indicada al evaluar datos operativos de timeouts.
