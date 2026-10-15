# photo-print-bridge

Puente HTTP en Go entre **PhotoPrint** (la app web) y las impresoras **HPRT CP4x** (CP4100…).
Un navegador no puede abrir sockets TCP/UDP crudos; este servicio sí, y traduce:

```
PhotoPrint (navegador) ── HTTP + SSE ──► photo-print-bridge ── TCP 9901 / 9100, UDP 6000 ──► impresora
```

Expone **el mismo contrato que el bridge Node** de `free-photo/server`, así que photo-print funciona
sin cambios. Además, cada error trae los campos de `shared.Error`.

## Arrancar

```sh
cp .env.example .env     # opcional: sólo para valores sensibles o para sobreescribir config.yaml
make run                 # o: go run ./cmd/bridge -config .
curl localhost:8787/api/state
```

Con photo-print: `BRIDGE_URL=http://127.0.0.1:8787 bun run dev`.

| Target | Qué hace |
|---|---|
| `make run` | arranca con `config.yaml` y `.env` |
| `make test` | tests con el detector de carreras |
| `make lint` | `gofmt` + `go vet` |
| `make build` / `make build-all` | binario local / Linux amd64, arm64, armv7 y riscv64 (Raspberry Pi, RISC‑V) |
| `make docker-up` / `make docker-down` | levanta / para con docker compose |

## Configuración

- **`config.yaml`** (raíz): todo lo no sensible. Incluye `app`, `logger`, `cors`, los puertos y tiempos de `printer`, y `activity.log_buffer`.
- **`.env`**: lo sensible (hoy no hay nada sensible) y overrides locales. Se carga con godotenv.
- Cualquier clave se sobreescribe con `BRIDGE_<SECCIÓN>_<CLAVE>`, por ejemplo `BRIDGE_APP_REST_ADDR=0.0.0.0:8787` o `BRIDGE_LOGGER_LEVEL=debug`. Viper aplica esos overrides.

Por defecto escucha en `127.0.0.1:8787`. Para que un contenedor de PhotoPrint llegue al bridge, usa `0.0.0.0:8787`.

El apagado es **graceful**: con SIGINT o SIGTERM, `internal/app/server.go` deja de aceptar conexiones, cierra los streams SSE y espera las peticiones en curso (hasta `app.shutdown_timeout`). Después libera la impresora.

## Arquitectura

Arquitectura hexagonal (puertos y adaptadores). El núcleo no importa nada de `adapters` ni de `app`.

```
cmd/bridge/main.go                  arranque: carga la config y ejecuta la app
internal/
├── app/                            composición (lo único que conoce todas las piezas)
│   ├── config.go                   config.yaml + .env + overrides BRIDGE_*
│   ├── services.go                 crea los adaptadores de salida y los inyecta en los servicios
│   ├── app.go                      handlers, cadena de middlewares y router
│   └── server.go                   servidor REST con apagado graceful
├── core/                           el hexágono: dominio puro, sin JSON ni red
│   ├── domains/printer             estado, status, etapas, eventos de trabajo, errores
│   ├── domains/photo               archivo de impresión validado (JPEG, tamaño)
│   ├── domains/network             ping, Wi‑Fi del equipo, gateway, redes
│   ├── ports/inbound.go            casos de uso: Printer, Photo, Diagnostics, Activity
│   ├── ports/outbound.go           PrinterDriver, PrinterSession (Monitor + Commander), NetworkInspector
│   └── services/                   flujos: conexión, reconexión, reintento, trabajos, logs
├── adapters/
│   ├── inbound/http/               routes.go (tabla de rutas), handlers, sse, dto
│   │   ├── middleware/             middleware.go (interfaz Middleware y Chain) + un archivo por middleware
│   │   └── response/               errores HTTP y escritura de JSON compartidos por handlers y middlewares
│   └── outbound/
│       ├── cp4x/                   protocolo de la impresora (la librería cp4x adaptada a los puertos)
│       └── hostnet/                ping, ip y nmcli del sistema
└── shared/                         logger (zerolog), errores, contexto
```

### Patrones

- **Puertos y adaptadores.** El comportamiento de la librería cp4x está definido como interfaces en `core/ports` (`PrinterDriver`, `PrinterSession`). `adapters/outbound/cp4x` las implementa usando los tipos del dominio. Siguiendo ISP, `PrinterSession` se compone de `PrinterMonitor` (leer) y `PrinterCommander` (actuar).
- **Cadena de responsabilidad** (`middleware/middleware.go`): cada `Middleware` implementa `Handle(w, r, next)`. Hace su trabajo y pasa la petición al siguiente eslabón, o la responde y corta la cadena (como CORS con el preflight). `Chain.Then` enlaza los eslabones en orden: recovery → request-id → logging → cors → router.
- **Tabla de rutas como fuente de verdad** (`routes.go`): cada `Route` declara su `OperationID`, método, ruta, resumen, tag, parámetros, cuerpo y respuestas, con el DTO como esquema, además del handler. El router se construye sólo a partir de esa tabla, así que la información para generar un OpenAPI ya está ahí.
- **Publicador/suscriptor** (`ActivityService`): los servicios publican estado, trabajos y logs, y cada cliente SSE es un suscriptor.
- **DTOs en el adaptador**: el dominio no tiene tags JSON; `dto.go` traduce al contrato del bridge Node.

## API

| Método | Ruta | Respuesta |
|---|---|---|
| GET | `/api/state` | estado de la conexión (`{"connected":false}` sin impresora) |
| GET | `/api/events` | SSE: `state`, `job`, `log`; el primero es el estado actual |
| GET | `/api/logs` | últimas líneas del protocolo `[{t, msg}]` |
| GET | `/api/discover?timeout=&target=` | impresoras encontradas por UDP `[{ip, name, sn}]` |
| POST | `/api/connect` `{ip, name?, sn?, wakeMs?}` | estado; si falla y hay `sn`, la busca en la LAN por su número de serie |
| POST | `/api/disconnect` | estado desconectado |
| POST | `/api/print` (cuerpo `image/jpeg`) | `{ok, unconfirmed?}` cuando la impresora termina |
| POST | `/api/cancel` | `{cancelled}` |
| GET | `/api/link?ip=` | `{ip, ping, pcWifi, heartbeat}` |
| GET | `/api/gateway` | `{gateway, dev, source, wifi}` (en modo hotspot, la propia impresora) |
| GET | `/api/wifi/scan` | `[{ssid, signal, freqMHz, security, band24}]` |

Errores: `{"error": "<mensaje>", "module": …, "internal_code": …, "message": …, "http_code": …}`.
El texto de `error` es el mismo que el del bridge Node, porque photo-print lo traduce por ese texto.

| Módulo | Códigos |
|---|---|
| `printer` | 1001 IP inválida · 1003 sin impresora · 1005 ocupada · 1006 trabajo en curso · 1007 sin respuesta a la reserva · 1008 conexión perdida · 1009 timeout · 1010 cancelada · 1100 no se pudo conectar · 1101 envío atascado · 1102 trabajo fallido |
| `photo` | 2001 no es JPEG · 2002 demasiado grande |
| `network` | 3001 escaneo no disponible · 3002 sin red Wi‑Fi |
| `http` | 4001 no encontrado · 4002 JSON inválido · 4003 error interno |

## Docker

```sh
docker compose up -d --build   # http://<esta máquina>:8787
```

- **`network_mode: host`**: el broadcast UDP de descubrimiento y la impresora de la LAN no se alcanzan a través del NAT de Docker.
- **Socket de D-Bus y `apparmor:unconfined`**: `nmcli` habla con el NetworkManager del host, que da los datos Wi‑Fi de `/api/link`, el gateway en modo hotspot y `/api/wifi/scan`. El perfil AppArmor por defecto de Docker bloquea D-Bus. Si no necesitas esos datos, quita esas dos líneas.
- Con el PhotoPrint dockerizado: `BRIDGE_URL=http://host.docker.internal:8787` en su `.env`.

## Tests

Todos los tests son *table-driven*: un slice `testCases` evaluado en un `for`.

- **Dominio:** etapas, status y mensajes de error.
- **Protocolo:** tramas, parser y status.
- **Medidor de latencia.**
- **Sesión** contra una impresora falsa por TCP: impresión completa, sin etapas, ocupada, sin papel, atasco, cancelada en la impresora y por el cliente.
- **Servicios** con puertos falsos: conexión, búsqueda por número de serie, reintento tras reconexión, cancelación.
- **Handlers y middlewares** con `httptest`, más la tabla de rutas y la configuración.

```sh
make test
```
