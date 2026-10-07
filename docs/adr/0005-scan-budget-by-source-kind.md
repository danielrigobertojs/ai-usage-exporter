# ADR-005: El presupuesto de escaneo por bytes no aplica a fuentes SQLite

- Fecha: 2026-10-07
- Estado: Aceptado
- Revisión: si aparece un formato consultable (no JSONL) donde el tamaño del
  fichero sí prediga el coste de la consulta

## Contexto

`internal/provider/discover.go` aplica dos topes por bytes a todo candidato
que encuentra, sin distinguir `SourceKind`: `MaxBytesFile` (256 MiB por
fichero) descarta cualquier candidato que lo supere, y `MaxTotalBytes`
(4 GiB) deja de admitir candidatos una vez que la suma acumulada de tamaños
los supera.

Medido el 2026-10-07 sobre `main` en `f737887` (con JCB-321 y JCB-322 ya
fusionados) contra los datos reales de `Daniels-MBP.lan`:

```
claude-code  files=30   skipped=0   events=2507    tokens=309451691   parse_errors=0
codex        files=275  skipped=0   events=1934    tokens=57828243    parse_errors=0
opencode     files=1    skipped=1   events=0       tokens=0           parse_errors=0
```

El provider de OpenCode no tiene ningún bug de parseo — JCB-322 ya corrigió
su consulta — pero nunca llega a ejecutarse: `opencode.db` pesa 2.75 GB en
esa máquina, 10.2x por encima de `MaxBytesFile`, y `Discover` lo descarta
antes de que `Parse` lo vea. `skipped=1` con `events=0` es la firma exacta
de este fallo.

## Por qué el tope por bytes es el control equivocado para SQLite

`MaxBytesFile` existe para acotar cuánto tiene que leer un `Provider.Parse`
*en streaming* antes de emitir el primer evento — ver el comentario de
`Budget` en `discover.go`: sin él, un JSONL de 2 GB convierte servir el
primer `/metrics` en una espera de varios minutos. Para un formato que se
lee secuencialmente de principio a fin, el tamaño del fichero **es**
literalmente el trabajo que `Parse` va a hacer, así que es el control
correcto.

Una fuente `SourceSQLite` no se lee así. `opencode.Parse` abre el fichero
con `modernc.org/sqlite` en modo `mode=ro` y ejecuta una consulta indexada
(`SELECT ... FROM message JOIN session ... ORDER BY m.id`); el driver pagina
el fichero bajo demanda a través del motor B-tree de SQLite, nunca carga el
`.db` completo en memoria. El tamaño en disco de un `opencode.db` no predice
ni cuánta memoria usa esa consulta ni cuánto tarda — lo que importa es
cuántas filas de rol `assistant` contiene y qué tan fragmentado está el
índice, ninguno de los dos derivable de `info.Size()`.

Contar esos bytes además producía un segundo efecto equivocado:
`MaxTotalBytes` (4 GiB) se consumía al ~69 % con este único fichero,
dejando sin presupuesto a los *demás* providers del mismo escaneo — aunque
hoy cada llamada a `Discover` parte de un `totalBytes` propio por invocación
y `scan.Run` llama a `Discover` una vez por provider, de forma que este
segundo efecto no se manifiesta todavía de forma cruzada entre providers.
Sigue siendo la dimensión equivocada para medir el coste de una fuente que
no se va a leer completa, y una futura implementación que comparta
presupuesto entre providers en el mismo escaneo heredaría el problema si no
se corrige aquí primero.

## Decisión

**Las fuentes `SourceSQLite` quedan exentas de los dos topes por bytes**
(`MaxBytesFile` y la contabilidad de `MaxTotalBytes`) en
`internal/provider/discover.go`. `Deadline` y `MaxFiles` siguen aplicando
sin excepción — son la guarda real para esta fuente, no el tamaño del
fichero.

```go
if d.Kind != SourceSQLite && info.Size() > b.MaxBytesFile {
    // ... descartar
}
...
countsTowardTotal := c.Kind != SourceSQLite
```

Los formatos que sí se leen en streaming (`SourceJSONL`, `SourceJSON`) no
cambian: siguen acotados por ambos topes exactamente como antes.

## Alternativas consideradas

1. **Exentar `SourceSQLite` de los topes por bytes (elegida).** Mínima,
   dirigida, restaura el provider sobre datos reales, y deja el control en
   la dimensión que sí corresponde a esta fuente (tiempo, vía `Deadline`).
   Desventaja: una base de datos patológica (índices corruptos, un disco de
   red muy lento) podría hacer lenta la consulta; lo acota el `Deadline`
   del presupuesto y el `busy_timeout(2000)` que ya lleva el DSN de
   `opencode.DSN`.
2. **Un tope propio y mucho mayor para SQLite** (p. ej. `MaxBytesSQLite`,
   16 GiB). Conserva una guarda explícita por tamaño, pero es un número
   arbitrario que una base de datos futura puede volver a superar, y sigue
   midiendo una dimensión que no es el coste real de una fuente consultada
   por índice. Descartada.
3. **Presupuesto por `SourceKind`** (un `map[SourceKind]int64` de topes).
   La más general — cubriría un futuro formato con su propio perfil de
   coste sin tocar `Discover` otra vez — pero es superficie de
   configuración nueva para la única necesidad real de hoy (un kind exento,
   no varios topes distintos). Prematura. Se reconsidera si aparece un
   segundo formato con un perfil de coste propio.
4. **Dejarlo como está.** Descartada: publica un MVP donde uno de los tres
   providers del alcance queda permanentemente mudo sobre datos reales,
   algo que ya falló dos veces antes (JCB-321, JCB-322) por la misma causa
   raíz — "CI verde, cero eventos sobre datos reales" — un nivel más abajo,
   en el presupuesto de escaneo en vez de en el parser.

## Consecuencias

- `Discover` ahora puede devolver una fuente `SourceSQLite` de cualquier
  tamaño; `Provider.Parse` sigue siendo responsable de acotar su propio
  trabajo (en OpenCode, vía `busy_timeout(2000)` en el DSN y respetando
  `ctx` en el bucle de filas).
- `MaxTotalBytes` deja de reflejar "bytes totales de todo lo encontrado" y
  pasa a significar "bytes totales de fuentes que se leen en streaming";
  una fuente SQLite nunca contribuye a ese acumulado ni lo consume.
- Un futuro cambio que comparta `Budget`/`MaxTotalBytes` entre providers
  dentro de un mismo `scan.Run` (hoy cada llamada a `Discover` parte de un
  contador propio) hereda esta exención automáticamente, sin tener que
  redescubrir el problema de JCB-324.
- Esta decisión no reabre ni modifica ADR-001: sigue sin haber estado
  agregado persistido entre arranques; esto es exclusivamente sobre qué
  cuenta como "bytes a presupuestar" dentro de un único escaneo.

## Condición de reapertura

Reabrir esta decisión si aparece un formato consultable (no streameado)
donde el tamaño del fichero en disco sí prediga de forma fiable el coste de
leerlo — en ese caso, pasar a la alternativa 3 (presupuesto por
`SourceKind`) en vez de añadir una exención ad-hoc más.
