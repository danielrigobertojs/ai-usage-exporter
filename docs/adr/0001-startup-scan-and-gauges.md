# ADR-001: Parseo al arranque y gauges en lugar de counters

- Fecha: 2026-10-02
- Estado: Aceptado
- Revisión: reconsiderar si se necesita continuidad de serie entre reinicios (ver "Condición de reapertura")

## Contexto

`ai-usage-exporter` lee los logs locales de agentes de IA (Claude Code, Codex CLI,
OpenCode, y en el futuro otros) y expone métricas de uso en un endpoint `/metrics`
para que Prometheus las scrapee.

Esos logs no son un stream continuo que el exporter pueda seguir con un offset
incremental de forma simple y uniforme entre proveedores: son JSONL que crecen,
bases SQLite en modo WAL, y en el caso de Claude Code, un directorio que
**autoborra entradas a los 30 días**. Un mensaje puede además reaparecer por
compactación, `/resume` o forks de sesión (ver la invariante de deduplicación
por id de mensaje).

Dado ese sustrato, hay dos decisiones de diseño encadenadas que hay que fijar
antes de escribir cualquier provider:

1. ¿Cuándo se leen los logs: una vez al arrancar, o en un bucle continuo con
   estado persistente entre lecturas?
2. ¿Qué tipo de métrica de Prometheus modela "tokens usados en la ventana de
   7 días": un counter monotónico, o un gauge recalculado en cada scan?

## Decisión

**El binario parsea los logs una sola vez, al arrancar el servicio.** No hay
bucle de reescaneo, no hay watcher de filesystem, no hay estado agregado
persistido entre arranques (ni en disco ni en una base propia). El snapshot
resultante se sirve en `/metrics` tal cual hasta el siguiente arranque del
proceso.

**Todas las series de uso se exponen como gauges agregados por ventana**
(`24h`, `7d`, `30d`, `mtd`, `all`), calculados en el momento del escaneo a
partir del historial completo disponible en disco — nunca como counters con
sufijo `_total`. Se añaden gauges de frescura (`ai_usage_scan_timestamp_seconds`,
`ai_usage_last_event_timestamp_seconds`) para que el operador pueda saber qué
tan viejo es el snapshot servido.

### Por qué un counter es el modelo equivocado aquí

Un counter de Prometheus solo es válido si Prometheus puede asumir que, salvo
reinicio del proceso que lo expone, **nunca baja**. `rate()` y `increase()`
dependen de esa invariante para detectar resets.

Aquí esa invariante no se sostiene incluso dentro de la vida de un solo
proceso en ejecución continua, porque el proceso no vuelve a escanear — pero
sí se rompe de forma garantizada **entre** arranques: si el exporter se
reinicia (deploy, crash, restart de contenedor) y Claude Code ya purgó JSONL
de hace más de 30 días, el total re-derivado del historial en disco en el
segundo arranque puede ser **menor** que el que se sirvió justo antes de
reiniciar. Prometheus interpretaría esa caída como un reset de counter
(correcto para un proceso que reinició su contador interno a cero, incorrecto
para uno que remide un historial parcialmente podado) y `rate()` produciría
picos espurios o valores negativos saneados a cero, es decir, basura.

Un gauge no tiene ese problema: "el total de tokens de la ventana `7d` según
el historial local en el momento de este escaneo" es una afirmación verdadera
en cada arranque, incluso si el historial que la sustenta cambió entre un
arranque y el siguiente. Un gauge nunca promete monotonicidad, así que no hay
contrato que romper.

## Alternativas descartadas

**Counters + estado persistente en SQLite propio (store agregado del
exporter).** Permitiría acumular un total verdaderamente monotónico
independiente de la purga de los logs fuente, y sería el diseño correcto si
se necesitara continuidad de serie histórica más allá de lo que los logs
crudos retienen. Se descarta para este proyecto porque:

- Introduce estado mutable que el exporter debe mantener consistente con una
  fuente que él no controla (los logs de cada agente), con todos los modos de
  fallo de una migración o corrupción de ese store.
- Contradice el invariante de lectura no destructiva y de simplicidad: el
  exporter deja de ser "snapshot sin estado" y pasa a ser un sistema con su
  propia base de datos que hay que operar, respaldar y versionar.
- No hay un requisito de producto hoy que necesite series históricas más
  allá de lo que los propios logs retienen (`all` ya cubre "todo lo que el
  historial local todavía tiene").

## Consecuencias

- Los dashboards de Grafana que consuman estas métricas deben usar los
  gauges directamente (`ai_usage_tokens{window="7d"}`) y no `rate()` /
  `increase()` sobre ellos — son snapshots, no acumuladores.
- Un reinicio del proceso hace que el `/metrics` servido cambie de golpe al
  nuevo snapshot; no hay transición suave ni interpolación.
- El dato más viejo que `/metrics` puede reflejar es exactamente lo que el
  log fuente todavía conserva en disco en el momento del escaneo — si Claude
  Code ya autoborró una sesión, esa sesión ya no existe para ninguna ventana,
  incluyendo `all`.
- Los providers deben deduplicar por id de mensaje al construir el snapshot
  (no por línea de log), para que la compactación y los forks de sesión no
  inflen los totales de una sola pasada de parseo.
- Para refrescar el snapshot hay que reiniciar el proceso; no hay endpoint
  de rescan ni señal (`SIGHUP`, etc.) en el alcance actual.

## Condición de reapertura

Reabrir esta decisión si aparece un requisito real de continuidad de serie
entre reinicios (por ejemplo, alertas basadas en tendencia que no toleren el
salto de un reinicio, o la necesidad de retener históricos más allá de lo que
los logs fuente conservan) **y** el equipo acepta el coste operativo de un
store propio descrito arriba.
