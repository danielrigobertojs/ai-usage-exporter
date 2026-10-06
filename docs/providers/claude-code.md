## Provider: Claude Code

`internal/provider/claudecode`. `tool` label value: `claude-code`.

### Ruta

```
~/.claude/projects/<project-slug>/<session-uuid>.jsonl
```

Reubicable con `CLAUDE_CONFIG_DIR` (sustituye por completo la resolución de
`$HOME` habitual, no se añade a ella). Un archivo por sesión; una línea JSON
por entrada.

### Retención de 30 días — leer antes de interpretar la ventana `all`

Claude Code autoborra sesiones de este directorio a los 30 días (configurable
por el usuario). Esto no es un detalle de implementación: cambia lo que la
ventana `all` de este provider significa.

- `all` **no** es "todo el historial de uso de Claude Code" — es "lo que
  todavía existe en disco en el momento de este escaneo". Una sesión borrada
  por retención deja de contar para *cualquier* ventana, incluida `all`.
- Por esto [ADR-001](../adr/0001-startup-scan-and-gauges.md) modela las series
  de uso como gauges recalculados en cada arranque, nunca como counters: un
  total re-derivado tras una purga de retención puede ser **menor** que el
  servido antes del reinicio, y un counter que baja es indistinguible de un
  reset para `rate()`/`increase()`.
- Un dashboard que lea `ai_usage_tokens{tool="claude-code", window="all"}`
  como "uso acumulado de por vida" está mal interpretando la métrica.

### Mapeo de campos

Solo las entradas `type == "assistant"` con un bloque `message.usage` son
relevantes; todo lo demás (entradas de usuario, resultados de herramienta,
resúmenes de compactación) se descarta antes de decodificarse por completo.

| JSONL | `model.UsageEvent` |
|---|---|
| `uuid` | `Key.MessageID` |
| `sessionId` (o el nombre del archivo sin extensión, si falta) | `Key.SessionID` |
| `cwd`, normalizado (o el slug del directorio padre, si falta) | `ProjectID` |
| `message.model` | `Model` |
| `timestamp` (RFC3339, con o sin fracción de segundo), convertido a UTC | `Timestamp` |
| `message.usage.input_tokens` | `Tokens[TokenInput]` |
| `message.usage.output_tokens` | `Tokens[TokenOutput]` |
| `message.usage.cache_read_input_tokens` | `Tokens[TokenCacheRead]` |
| `message.usage.cache_creation_input_tokens` | `Tokens[TokenCacheWrite]` |
| número de bloques `tool_use` en `message.content` | `ToolCalls` |

`ProjectID` solo se convierte en el label `project` si ese label se activa
explícitamente — ver `docs/metrics.md`; por defecto queda sin usar fuera de
logs.

### Deduplicación

La compactación y `/resume` hacen que la misma entrada `assistant` (mismo
`uuid`) reaparezca en el JSONL. Este provider **no deduplica**: emite ambas
apariciones con el mismo `EventKey`, y `internal/aggregate.Aggregator` es el
único punto que descarta la repetida. Ver
`internal/provider/claudecode/testdata/session_compacted.jsonl`.

### Robustez de parseo

- Líneas malformadas, sin `usage`, o con `timestamp` inválido se saltan sin
  abortar el resto del archivo — ver `testdata/session_malformed.jsonl`.
- Una línea que supera los 8 MiB se descarta sin cargarla en memoria y sin
  detener el escaneo del resto del archivo.
- Nunca se decodifica el texto de un mensaje ni la entrada/salida de una
  herramienta: el struct de decodificación no tiene campo para ello, así que
  no hay ruta posible por la que ese contenido llegue a un `UsageEvent`.
