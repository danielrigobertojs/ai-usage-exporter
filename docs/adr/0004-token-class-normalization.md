# ADR-004: Normalización de clases de tokens por provider

- Fecha: 2026-10-06
- Estado: Aceptado
- Revisión: al añadir un provider nuevo, o si un provider upstream cambia el
  anidamiento de sus contadores

## Contexto

`docs/metrics.md` establece el invariante de que las cinco clases de
`token_type` (`input`, `output`, `cache_read`, `cache_write`, `reasoning`)
**particionan** los tokens de un evento: cada token se cuenta en exactamente
una clase. El invariante existe porque `pricing.CostUSD` suma por clase de
forma independiente, así que un token contado dos veces se cobra dos veces, a
dos tarifas distintas.

Lo que el contrato no decía es que **cada provider usa una convención de
anidamiento distinta en origen**, y que dos de las tres son incompatibles
entre sí. Esto no se detectó durante JCB-310/311/312 porque cada provider se
validó contra fixtures sintéticos escritos por el mismo autor que el parser:
el fixture codificaba la misma suposición que el código, y el test pasaba.

Los dos providers que no son Claude Code resultaron no emitir ningún evento
sobre datos reales (JCB-311, JCB-312), y al verificar el esquema real
aparecieron las convenciones de abajo.

## Decisión

**La normalización a clases disjuntas es responsabilidad del provider, no del
agregador ni del collector.** Cada `Parse` emite `model.UsageEvent.Tokens` ya
particionado; nada aguas abajo re-interpreta, resta ni deduce.

Convenciones verificadas en origen, con la evidencia que las sostiene:

| Provider | `cache` vs `input` | `reasoning` vs `output` | Fuente del contador |
| --- | --- | --- | --- |
| Claude Code | disjunto en origen | n/a | campos nativos de la API en JSONL |
| Codex | **anidado**: `cached_input_tokens` ⊂ `input_tokens` | **anidado**: `reasoning_output_tokens` ⊂ `output_tokens` | `event_msg`/`token_count`, acumulado por sesión |
| OpenCode | disjunto en origen | disjunto en origen | `message.data`, delta por mensaje |

Evidencia Codex, sobre 1919 registros `token_count` de 191 rollouts reales,
sin contraejemplos: `total_tokens == input_tokens + output_tokens` en 1919/1919
con `cached_input_tokens > 0`, y `cached_input_tokens > input_tokens` en 0
casos; `total_tokens == input_tokens + output_tokens` en 1913/1913 de los
registros con `reasoning_output_tokens > 0`.

Evidencia OpenCode, sobre 18.423 mensajes de asistente de una base real, con la
aritmética explicada al 100 %: 8977 cumplen
`total == input + output + cache.read + cache.write` (con `reasoning == 0`) y
8893 cumplen `total == input + output + cache.read + cache.write + reasoning`.
El anidamiento no ocurre nunca.

Por tanto Codex **resta** y OpenCode **no**:

```
# Codex: deshace los dos anidamientos sobre los deltas del acumulado
input      = max(0, Δinput_tokens  - Δcached_input_tokens)
cache_read = Δcached_input_tokens
output     = max(0, Δoutput_tokens - Δreasoning_output_tokens)
reasoning  = Δreasoning_output_tokens

# OpenCode: mapeo directo, sin restas
input, output, reasoning, cache_read, cache_write = los cinco campos tal cual
```

Codex no expone `cache_write`; se omite en lugar de emitirse como 0, para que
la ausencia del dato se distinga de un valor medido de cero.

## Alternativas consideradas

- **Normalizar en el agregador, con una bandera de convención por provider.**
  Centraliza la resta en un sitio, pero mueve conocimiento específico del
  formato fuera del único paquete que ya lo tiene, y obliga a que
  `UsageEvent` transporte un estado intermedio no disjunto que viola el
  invariante del contrato mientras viaja. Descartada.
- **Añadir una clase `input_total` que incluya el cacheado.** Haría las sumas
  por provider más fáciles de comparar con las UIs nativas, pero rompe
  explícitamente la partición, que es la propiedad de la que depende el coste.
  Descartada.
- **Mantener el estado actual.** No es una opción: hoy el número principal
  del exporter sería incorrecto para dos de los tres providers del MVP.

## Consecuencias

- Cada provider carga con un test de invariante obligatorio: la suma de las
  cinco clases emitidas debe igualar el total declarado por la herramienta
  para esa sesión. Es el único test que detecta el doble conteo, y es el que
  faltaba en JCB-311 y JCB-312.
- Los fixtures de provider deben ser **extractos redactados de datos reales**,
  no sintéticos. Un fixture escrito desde la misma suposición que el parser no
  prueba nada; esa es la causa raíz de los dos defectos. Redactar significa
  sustituir `cwd`, instrucciones, argumentos de herramienta y contenido de
  mensajes por `REDACTED`, conservando solo timestamps, ids, modelo y
  contadores.
- Un provider nuevo no puede darse por terminado con tests verdes: hace falta
  una pasada sobre datos reales que demuestre `events > 0`. El subcomando
  `doctor` (JCB-315) expone eventos emitidos y tokens sumados por provider
  justamente para que "encontré ficheros y emití 0 eventos" sea visible en
  lugar de ser un cero silencioso.
- `docs/metrics.md` no cambia: el invariante de disjunción era correcto. Lo
  que faltaba era este mapeo por provider.
