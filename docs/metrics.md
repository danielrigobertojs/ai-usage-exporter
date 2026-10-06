# Contrato de métricas

Esta es la fuente de verdad para todas las métricas que `ai-usage-exporter`
expone en `/metrics`. Cualquier provider o collector que añada o modifique
una métrica debe actualizar este documento en el mismo PR.

Prefijo de namespace: `ai_usage_`. **Todas las métricas son gauges** — ver
[ADR-001](adr/0001-startup-scan-and-gauges.md) para la razón por la que las
series de uso nunca llevan sufijo `_total` ni se modelan como counters.

Los nombres de métrica son API pública: un rename rompe dashboards
existentes, así que se discute en el issue correspondiente antes de hacerse.

## Labels permitidos

Solo estos cuatro labels pueden aparecer en cualquier métrica de este
contrato:

| Label | Significado | Valores |
|---|---|---|
| `tool` | Agente de IA que generó el uso | `claude-code`, `codex`, `opencode`, ... |
| `model` | Modelo usado dentro de esa herramienta | p. ej. `claude-opus-4`, `gpt-5-codex` |
| `token_type` | Categoría de tokens contados | `input`, `output`, `cache_read`, `cache_write`, `reasoning` |
| `window` | Ventana temporal agregada en el momento del escaneo | `24h`, `7d`, `30d`, `mtd`, `all` |

`session_id` **nunca** es un label, en ninguna circunstancia, bajo ninguna
métrica futura. Es la trampa clásica de cardinalidad en exporters de este
tipo: miles de series muertas que Prometheus nunca vuelve a necesitar
después del escaneo que las generó. Si un dato necesita granularidad de
sesión, va a logs (`log/slog`), nunca a una label.

`project` es **opt-in y apagado por defecto** (`labels.project: false` en la
config). Cuando se activa, el valor se normaliza y se **trunca a 48
caracteres**; nunca se usa la ruta completa del proyecto en disco.

### Presupuesto de cardinalidad

Cota declarada para el peor caso previsto:

```
5 tools × 30 modelos × 5 token_type × 5 window = 3.750 series
```

Esa cota es una regla de diseño, no una sugerencia: cualquier label nuevo que
se proponga debe calcular su impacto en esta multiplicación antes de
aceptarse.

## Métricas

### `ai_usage_tokens`

- **Tipo:** gauge
- **Labels:** `tool`, `model`, `token_type`, `window`
- **Unidad:** tokens (conteo, sin unidad base de Prometheus)
- **Significado:** número de tokens de tipo `token_type` consumidos por
  `model` dentro de `tool`, agregados sobre la ventana `window`, según el
  historial local disponible en el momento del escaneo. Deduplicado por id
  de mensaje, nunca por línea de log cruda.

#### Invariante: las clases de `token_type` son disjuntas

Las cinco clases de `token_type` particionan los tokens de un evento: cada
token se cuenta **exactamente una vez**, bajo una sola clase. En concreto,
`input` significa **input no cacheado**: excluye siempre lo que ya se contó
bajo `cache_read` y `cache_write`.

Esta es una invariante del exporter, no de las herramientas que lee, y cada
provider es responsable de cumplirla al normalizar su formato nativo:

- Las herramientas cuyo formato ya reporta el input **excluyendo** el
  cacheado (p. ej. Claude Code, cuyo `input_tokens` es disjunto de
  `cache_read_input_tokens`) mapean los campos directamente.
- Las herramientas cuyo formato reporta el input **incluyendo** el cacheado
  (p. ej. Codex, cuyo `last_input_tokens` contiene a `last_cached_tokens`;
  es la convención de la API de OpenAI, donde `cached_tokens` es un detalle
  de `prompt_tokens`) deben restar la parte cacheada antes de emitir
  `input`, con suelo en cero.

Hay dos razones por las que esto es contrato y no detalle de cada provider:

1. **Coste.** `ai_usage_cost_usd` suma por clase y los tokens cacheados
   tienen tarifa propia, típicamente un orden de magnitud menor. Contar el
   mismo token en `input` y en `cache_read` sobrestima el coste, y lo hace
   más cuanto mejor funciona la caché — justo al contrario de lo que el
   usuario espera ver.
2. **Comparabilidad entre herramientas.** Sin esta invariante, `sum by
   (tool) (ai_usage_tokens)` no es comparable entre dos `tool` distintos,
   que es la pregunta principal que este exporter existe para responder.

Corolario verificable para cualquier provider nuevo: para un mismo evento,
la suma de las cinco clases debe igualar el total de tokens que la
herramienta reporta por su cuenta, cuando lo reporta.

### `ai_usage_cost_usd`

- **Tipo:** gauge
- **Labels:** `tool`, `model`, `window`
- **Unidad:** USD
- **Significado:** coste estimado en dólares atribuible a `model` dentro de
  `tool` en la ventana `window`, derivado de los tokens consumidos y la
  tabla de precios vigente en el momento del escaneo.
- **Ausente**, nunca en `0`, para un par (`tool`, `model`) que el catálogo de
  precios no conoce — un modelo nuevo no es un modelo gratis.

#### Reconciliación con el coste nativo de un provider

Algunos formatos (OpenCode, por ejemplo) ya traen un campo de coste propio.
**El catálogo de precios siempre gana sobre ese coste nativo**: todas las
herramientas se comparan bajo la misma regla de tarificación, en vez de que
cada una aporte su propia noción de dólar. En la práctica esto ni siquiera
es una decisión que el collector tenga que arbitrar en tiempo de ejecución:
`model.UsageEvent` no tiene un campo de coste, así que `ai_usage_cost_usd`
solo puede derivarse del catálogo — no hay otra fuente con la que
reconciliar.

### `ai_usage_sessions`

- **Tipo:** gauge
- **Labels:** `tool`, `window`
- **Unidad:** sesiones (conteo)
- **Significado:** número de sesiones distintas de `tool` con al menos un
  evento dentro de la ventana `window`. Una sesión cuenta una sola vez
  incluso si fue reanudada (`/resume`) o bifurcada.

### `ai_usage_tool_calls`

- **Tipo:** gauge
- **Labels:** `tool`, `window`
- **Unidad:** llamadas (conteo)
- **Significado:** número de invocaciones de herramientas (tool calls, en el
  sentido de function/tool calling del modelo) registradas por `tool` dentro
  de la ventana `window`.

### `ai_usage_last_event_timestamp_seconds`

- **Tipo:** gauge
- **Labels:** `tool`
- **Unidad:** segundos (Unix timestamp UTC)
- **Significado:** timestamp del evento más reciente encontrado en el
  historial local de `tool` en el momento del escaneo. Permite detectar
  herramientas inactivas o fuentes de datos que dejaron de recibir eventos.

### `ai_usage_provider_available`

- **Tipo:** gauge
- **Labels:** `tool`
- **Unidad:** booleano (`0` o `1`)
- **Significado:** `1` si el provider de `tool` pudo localizar y leer su
  fuente de datos en este escaneo, `0` si la fuente no existe, no es
  legible, o el parseo falló por completo. No distingue "nunca instalado"
  de "falló al leer" — eso va a logs.

### `ai_usage_scan_timestamp_seconds`

- **Tipo:** gauge
- **Labels:** ninguno
- **Unidad:** segundos (Unix timestamp UTC)
- **Significado:** momento en que el proceso completó el escaneo cuyo
  snapshot está sirviendo `/metrics` ahora. Como el parseo ocurre una sola
  vez al arrancar ([ADR-001](adr/0001-startup-scan-and-gauges.md)), este
  valor es esencialmente el tiempo de arranque del proceso y no cambia hasta
  el siguiente reinicio.

### `ai_usage_scan_duration_seconds`

- **Tipo:** gauge
- **Labels:** ninguno
- **Unidad:** segundos
- **Significado:** cuánto tardó el escaneo completo de todos los providers
  al arrancar. Útil para detectar degradación cuando el volumen de logs
  locales crece.

### `ai_usage_scan_files`

- **Tipo:** gauge
- **Labels:** `tool`
- **Unidad:** archivos (conteo)
- **Significado:** número de archivos (o, para providers SQLite, de
  filas/sesiones procesadas) que el provider de `tool` leyó en este escaneo.

### `ai_usage_scan_errors`

- **Tipo:** gauge
- **Labels:** `tool`
- **Unidad:** errores (conteo)
- **Significado:** número de archivos o registros que el provider de `tool`
  no pudo parsear en este escaneo (formato inesperado, archivo corrupto,
  etc.). Un valor mayor que cero no implica necesariamente
  `ai_usage_provider_available=0` si el provider pudo recuperarse y seguir
  con el resto de la fuente.

### `ai_usage_build_info`

- **Tipo:** gauge
- **Labels:** `version`, `commit`, `go_version`
- **Unidad:** adimensional, siempre `1`
- **Significado:** metadato de build del binario en ejecución, siguiendo la
  convención estándar de Prometheus (`*_build_info`). El valor es siempre
  `1`; la información está en las labels, no en el valor.

## Privacidad

Ninguna métrica de este contrato, presente o futura, puede exponer contenido
de prompts, salidas de modelo o de herramientas. El exporter lee
exclusivamente metadatos de uso (tokens, modelo, timestamps, conteos). Esto
es un contrato de producto, no una preferencia de implementación — ver la
invariante de privacidad en las instrucciones del proyecto.
