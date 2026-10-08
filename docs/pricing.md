# Catálogo de precios (`internal/pricing`)

`internal/pricing` resuelve tarifas en USD por token para un par
(`tool`, `model`) y las aplica a los contadores de un `model.UsageEvent`
para producir `ai_usage_cost_usd`. Este documento es la fuente de verdad
de ese paquete: cualquier cambio en el orden de resolución, el formato de
los archivos de precios o las rutas por plataforma debe actualizarlo en el
mismo PR.

**Lo que expone esta métrica es una estimación a precio de lista público en
USD del proveedor de primera parte, no una factura.** No reconcilia
descuentos contractuales, planes de suscripción, créditos, redondeo por
proveedor, ni el campo `cost` nativo que algunos formatos (por ejemplo
OpenCode) ya incluyen — esa reconciliación es una decisión del collector que
consume este paquete, documentada allí.

## Orden de resolución

```
overlay del usuario  >  caché de models.dev  >  tabla embebida
```

`Load` resuelve en ese orden y **nunca devuelve error** por una causa de
red, de caché o de datos: cada nivel que falla degrada silenciosamente al
siguiente, y la tabla embebida (compilada en el binario vía `go:embed`) es
el suelo que garantiza que siempre hay un precio que servir, o una
ausencia explícita (`Lookup` devuelve `false`) en vez de un `0` falso.

`Catalog.Source()` informa cuál es el nivel más alto que tiene datos
cargados — `"overlay"`, `"models.dev"` o `"embedded"` — no la procedencia
de cada modelo individual. Si el overlay del usuario redefine un solo
modelo, `Source()` ya reporta `"overlay"` aunque el resto de los modelos se
sigan resolviendo por niveles inferiores.

Dentro de cada nivel, `Lookup(tool, modelID)` prueba primero la clave
desnuda del modelo (`modelID`) y solo si no existe cae a la clave
cualificada por herramienta (`tool:modelID`). Un modelo ausente en los tres
niveles devuelve `(Rates{}, false)`: un modelo desconocido **no vale
$0**, debe quedar observable como "sin precio".

## Caché de models.dev

- Fuente: `https://models.dev/api.json` (dataset MIT de `sst/models.dev`;
  ver `NOTICE`).
- Caché local en `<Config.CacheDir>/models-dev-v1.json`. Por defecto,
  `Config.CacheDir` es `os.UserCacheDir()/ai-usage-exporter`.
- TTL de 24 h por defecto (`Config.TTL`), validado contra el `mtime` del
  archivo de caché — no hay metadatos adicionales.
- Si el refresco de red falla (error de conexión, status distinto de 200,
  cuerpo no parseable) y existe una caché previa, **esa caché sigue
  sirviendo** aunque esté vencida. Solo si no hay ninguna caché utilizable
  se cae a la tabla embebida.
- Una caché corrupta (bytes inválidos) se trata igual que una caché
  ausente: se ignora y se intenta el nivel siguiente, nunca hace panic.
- La petición manda `User-Agent: ai-usage-exporter/<version> (+<repo>)`,
  como cortesía identificable hacia quien mantiene el dataset gratis.
- Cuando un ID de modelo aparece bajo varios providers, se prefiere una lista
  ordenada de proveedores de primera parte (`anthropic`, `openai`, `opencode`,
  ...); si ninguno coincide, gana el provider con ID alfabéticamente primero.
  Es una heurística de procedencia, no una atribución de facturación. Ver
  ADR-006.

### Forzar modo offline

`Config.Offline = true` evita que `Load` haga ninguna petición de red: usa
la caché local si existe (incluso vencida) o cae directamente a la tabla
embebida. Útil para pruebas, entornos air-gapped, o para evitar el costo
de una llamada de red en cada arranque cuando el operador prefiere refrescar
el precio manualmente.

## El overlay del usuario

El overlay tiene **el mismo formato** que la tabla embebida, para que un
usuario pueda copiar `embedded.json` y editarlo:

```json
{
  "version": 1,
  "models": {
    "claude-opus-5": {"input": 15.0, "output": 75.0, "cache_read": 1.5, "cache_write": 18.75}
  }
}
```

Los valores son **USD por millón de tokens** (igual que models.dev);
`Load` los divide por 1e6 al construir `Rates`. Un modelo que el overlay no
menciona sigue resolviéndose por los niveles inferiores — el overlay
sobreescribe por modelo, no reemplaza el catálogo entero.

### Ruta del overlay por plataforma

Por defecto, `Config.OverlayPath` es `<xdg_config>/ai-usage-exporter/pricing.json`,
resuelto con `os.UserConfigDir()` — nunca una ruta `~` construida a mano:

| SO | Ruta típica |
|---|---|
| Linux | `$XDG_CONFIG_HOME/ai-usage-exporter/pricing.json` (o `~/.config/...` si la variable no está definida) |
| macOS | `~/Library/Application Support/ai-usage-exporter/pricing.json` |
| Windows | `%AppData%\ai-usage-exporter\pricing.json` |

Un overlay ausente no es una condición de error: `Load` simplemente no
tiene nada que superponer. Un overlay presente pero corrupto se ignora de
la misma forma que una caché corrupta.

## Clases de token y `Rates`

`Rates` son USD por **un** token (nunca por millón), con un campo
independiente por clase: `Input`, `Output`, `CacheRead`, `CacheWrite`,
`Reasoning`. `CostUSD` suma cada clase de tokens del evento contra su
tarifa correspondiente de forma independiente — nunca mezcla clases ni
aplica una tarifa única a todos los tokens.

## Atribución

Los datos de precios derivan de `sst/models.dev` (licencia MIT). La
atribución obligatoria vive en `NOTICE` y, para la tabla embebida, en los
campos `retrieved_at` y `attribution` de `internal/pricing/embedded.json`.
