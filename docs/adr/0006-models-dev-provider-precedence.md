# ADR-006: Precedencia de proveedores de primera parte para models.dev

- Fecha: 2026-10-08
- Estado: Aceptado
- Revisión: cuando una instalación consuma el mismo ID de modelo por dos
  proveedores o gateways distintos

## Contexto

El catálogo de `models.dev` publica un mismo ID de modelo bajo proveedores de
primera parte y bajo revendedores. Hasta ahora `parseModelsDev` elegía la fila
del ID de provider alfabéticamente primero. En la instalación verificada, eso
elegía habitualmente `302ai`, una fila de revendedor que omitía tarifas de
`cache_read` y `cache_write`.

El 94 % de los tokens de la ventana de 30 días eran `cache_read`, por lo que
esas omisiones se valoraban a cero. La métrica `ai_usage_cost_usd` resultaba
$62.91 en vez de $279.33: una subestimación de aproximadamente 4.4x.

## Decisión

Para IDs desnudos que aparecen bajo varios providers, se recorren primero, en
orden, los proveedores de primera parte que representan los caminos de cobro
directos de los tools soportados:

```
anthropic, openai, opencode, google, xai, moonshotai, deepseek,
zhipuai, z-ai, mistral, meta, alibaba, qwen
```

Los providers que no estén en esa lista se recorren después por ID
alfabéticamente, conservando el fallback anterior. Una entrada sin `cost`
sigue sin crear una tarifa: es un modelo sin precio, no un modelo gratuito.
Una entrada `cost` con todas las clases a cero sigue siendo una tarifa cero
explícita.

## Alternativas consideradas

1. **Mantener el desempate alfabético.** Es sencillo, pero en la práctica
   seleccionaba revendedores que eliminan las tarifas de caché y publicaba un
   coste materialmente falso. Descartada.
2. **Elegir la fila con más clases de tarifa.** Evita una lista mantenida,
   pero puede seleccionar un revendedor más caro: para `claude-fable-5`, la
   fila más completa observada tenía input de $11/M frente a $10/M de
   Anthropic. Descartada.
3. **Cualificar el catálogo y cada evento por provider.** Es la atribución
   correcta para consumo por gateways, pero requiere cambiar el modelo de
   datos, los tiers embedded/cache/overlay y `Catalog.Lookup`. Se pospone.

## Consecuencias

- La estimación sube aproximadamente 4.4x en 30 días en la instalación que
  reveló el defecto; no es una regresión de uso sino la valoración de caché
  que faltaba.
- `ai_usage_cost_usd` sigue siendo precio de lista público, no una factura:
  puede diferir por descuentos, créditos y suscripciones.
- La heurística es determinista y conserva el fallback alfabético para
  providers no reconocidos.

## Condición de reapertura

Reemplazar esta heurística por IDs de modelo cualificados por provider cuando
una instalación consuma el mismo modelo a través de dos proveedores o
gateways distintos.
