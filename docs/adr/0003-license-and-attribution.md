# ADR-003: Licenciamiento Apache-2.0 y kit de atribución

- Fecha: 2026-10-05
- Estado: Aceptado
- Revisión: reconsiderar si el solicitante decide licenciamiento dual u
  open core (ver "Condición de reapertura")

## Contexto

El solicitante pidió open source con **reconocimiento permanente del
repositorio y del autor**. El bootstrap original (JCB-307) dejó un `LICENSE`
de Apache-2.0 ya en el repositorio, pero no el resto del kit de atribución
que la licencia por sí sola no garantiza.

MIT, la alternativa obvia más permisiva, no cumple el requisito: obliga a
conservar el aviso de copyright en copias y porciones sustanciales, pero no
tiene mecanismo de propagación de avisos a forks, no obliga a declarar
modificaciones, no dice nada de marcas, y permite que un fork rebrandee el
proyecto, entierre el `LICENSE` y cumpla legalmente mientras la atribución
queda invisible para cualquier usuario final.

## Decisión

**Apache License 2.0** como licencia del proyecto. Las cláusulas que
resuelven el requisito:

- **§4(b)** — los trabajos derivados deben llevar avisos prominentes de que
  se modificó el archivo.
- **§4(c)** — deben conservar los avisos de copyright, patente, marca y
  atribución del original.
- **§4(d)** — el archivo `NOTICE` de este repositorio se propaga: cualquier
  derivado distribuido debe incluir una copia legible de sus avisos, en su
  propio `NOTICE`, en la distribución de fuentes, **o en la salida que el
  derivado genera**. Esta última vía es la que usamos: `version.String()`
  incluye `internal/license.Attribution()`, así que el crédito viaja con el
  binario en ejecución, no solo con el código fuente.
- **§6** — no concede derechos de marca; el nombre del proyecto sigue siendo
  del autor. Documentado en `TRADEMARK.md`.
- Concesión expresa de patentes (§3), con terminación si alguien litiga por
  patentes contra el proyecto.

Titular del copyright: persona física **Daniel Rigoberto Jacobo Sandoval**
(decidido por el solicitante el 2026-10-02). Aplicado en `NOTICE`,
`internal/license`, las cabeceras SPDX de cada `.go`, y `CITATION.cff`.

Mecanismo de contribución: **DCO** (`Signed-off-by:` por commit, verificado
en CI), no CLA. Ver "Alternativas descartadas".

## Límite honesto

Apache-2.0 no obliga a un crédito visible en la interfaz de un producto
cerrado que incorpore este código como dependencia interna sin
redistribuirlo — §4 solo aplica a quien *distribuye* el Work o Derivative
Works. Ninguna licencia aprobada por la OSI logra eso de forma fiable. Las
que lo intentan (la cláusula publicitaria de BSD-4-clause, las licencias
"attribution assurance") son incompatibles con GPL, generan proliferación
de licencias y espantan la adopción. No se consideran.

Lo que sí logramos, y es lo que estaba al alcance de este ticket: la
atribución viaja con cada **distribución** del binario o del código
(`NOTICE`, cabeceras SPDX, `version.String()`, el `User-Agent` HTTP, y la
métrica `ai_usage_build_info` cuando exista el collector).

## Alternativas descartadas

**MIT.** Ver "Contexto" — no cumple el requisito de atribución persistente.

**Licencias con cláusula publicitaria o "attribution assurance" (ej.
BSD-4-clause).** Sí fuerzan un crédito más visible, pero son incompatibles
con GPL (proliferación de licencias, GPL es copyleft pero este proyecto no
lo es, así que no afecta directamente, pero sí afecta a cualquier
consumidor downstream que combine esta dependencia con software GPL) y la
FSF y Debian las señalan como problemáticas. Riesgo de adopción más alto que
el beneficio marginal de atribución que dan sobre Apache-2.0 §4(d).

**CLA en lugar de DCO.** Un CLA típicamente licencia o asigna el copyright
de la contribución al mantenedor más allá de lo que Apache-2.0 ya concede
en su §5, y es el mecanismo que habilitaría licenciamiento dual u open
core. Este proyecto no persigue ninguno de los dos (ver "Fuera de alcance"
del ticket que originó este ADR), así que el DCO — una atestación más
ligera, sin cesión adicional — es suficiente y reduce la fricción para
contribuir.

## Consecuencias

- `scripts/check-spdx.sh` en CI falla el build si algún `.go` pierde su
  cabecera SPDX/copyright — el mecanismo que sobrevive al copy-paste de un
  archivo suelto fuera de su historial de git.
- `scripts/gen-third-party.sh` en CI falla el build si una dependencia
  nueva es copyleft (GPL, AGPL, LGPL, MPL) o no tiene licencia detectable,
  y si `THIRD_PARTY_LICENSES.md` committeado queda desactualizado frente a
  `go.mod`.
- Cambiar los nombres de métrica ya expuestas sigue requiriendo discusión
  en el issue correspondiente — eso no lo cambia este ADR, pero
  `ai_usage_build_info` gana los labels `project` y `license` sin dejar de
  ser una sola serie.
- Si en el futuro se quiere licenciamiento dual u open core, este ADR debe
  reabrirse: implica sustituir DCO por CLA y es una decisión de negocio, no
  de ingeniería — explícitamente fuera de alcance aquí.

## Condición de reapertura

Reabrir si el solicitante decide perseguir licenciamiento dual u open core
(requeriría CLA en lugar de DCO), o si se decide registrar la marca ante una
oficina (IMPI u otra) — ambos son trámites de negocio/legales fuera del
alcance de este ticket.
