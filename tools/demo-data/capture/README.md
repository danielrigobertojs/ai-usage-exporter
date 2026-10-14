# Cómo se regeneran las capturas de `docs/images/`

Las cinco PNG del README **no** son capturas de una maquina real: salen de los
datos sinteticos de `tools/demo-data`, asi que ninguna imagen puede filtrar un
nombre de host, un gasto ni un prompt de quien las ejecuta.

Este directorio es el procedimiento ejecutable. Para regenerarlas:

```bash
# 1. Datos sinteticos para DOS maquinas demo (el panel "por instancia" necesita
#    mas de una, si no el diferencial de este ticket no se ve).
go run ./tools/demo-data -out /tmp/auecap/workstation
go run ./tools/demo-data -out /tmp/auecap/laptop

# 2. Un exporter por maquina. El puerto NO es 9477 a proposito: en una maquina
#    de desarrollo puede haber un exporter real escuchando ahi, y el objetivo de
#    este procedimiento es no tocarlo.
go build -o ./bin-aue-demo ./cmd/ai-usage-exporter
HOME=/tmp/auecap/workstation XDG_DATA_HOME=/tmp/auecap/workstation \
  ./bin-aue-demo -listen 0.0.0.0:19477 -timezone UTC &
HOME=/tmp/auecap/laptop XDG_DATA_HOME=/tmp/auecap/laptop \
  ./bin-aue-demo -listen 0.0.0.0:19478 -timezone UTC &

# 3. Prometheus y Grafana. El override de este directorio cambia solo el puerto
#    de host y el config de Prometheus; las imagenes y el provisioning son los
#    del repo.
cp deploy/.env.example deploy/.env
docker compose -f deploy/docker-compose.yml -f tools/demo-data/capture/docker-compose.yml up -d

# 4. Captura. Chrome headless por CDP, una pestana nueva por imagen.
./tools/demo-data/capture/shot.sh \
  'http://127.0.0.1:13000/d/ai-usage-live/965cb66?orgId=1&kiosk' \
  docs/images/dashboard-live.png 1600 430
# ... las demas en docs/images/dashboard-{by-instance,cost-by-model,token-classes,health}.png

# 5. Optimizar (la cota del ticket es 250 KB por imagen y 1 MB en total).
oxipng -o4 docs/images/*.png
```

## Por que CDP y no `--screenshot`

Chrome con `--screenshot --virtual-time-budget=N` dispara la captura cuando el
presupuesto virtual se agota. Grafana monta los paneles de forma perezosa, asi
que sale la barra superior y debajo ni un panel: es un dashboard vacio. `shot.sh`
navega por CDP, espera a que los paneles dibujen y solo entonces pide el
screenshot. Ademas abre **una pestana por imagen**: reutilizar la misma pagina
para la segunda navegacion deja el target colgado.

## Los nombres de host salen de aqui

`capture/prometheus.yml` fija `instance` a `demo-workstation` y `demo-laptop`
mediante el `instance` de cada `static_configs`. Por eso ninguna captura puede
mostrar el host real de quien la ejecuta, aunque el exporter este en un
`host.docker.internal`. **No borres ese bloque al regenerarlas.**

## Los datos envejecen: hay que capturar en los primeros ~45 minutos

El generador ancla los eventos entre 50 minutos en el pasado y el presente, asi
que la ventana `1h` empieza a vaciarse conforme pasan los minutos y se vacia
del todo alrededor del minuto 50. Si `report --window 1h` devuelve cero filas,
regenera los datos y vuelve a capturarte; no es un fallo de la captura.

Las cifras de las capturas salen de la salida del generador, no de una sesion
real:

```bash
HOME=/tmp/auecap/workstation XDG_DATA_HOME=/tmp/auecap/workstation \
  ./bin-aue-demo report --window 1h --output json
```

Con los datos recien generados y dos maquinas, eso da 17 filas, 44.520 tokens y
0,2964 USD por maquina: 89.040 tokens y 0,5928 USD en el panel "Ahora", que es
lo que muestra `dashboard-live.png`.

## Parar todo

```bash
docker compose -f deploy/docker-compose.yml -f tools/demo-data/capture/docker-compose.yml down -v
kill <los dos PIDs del exporter>
rm -f deploy/.env
```