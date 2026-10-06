# Nexus

Servicio único de integración entre SGP y aplicaciones externas (SAP, PEL, AMD, etc.), desarrollado en Go.

Documentación de especificación completa en [`docs/`](docs/README.md). Plan de trabajo de la prueba de concepto en [`docs/10-plan-de-trabajo-poc.md`](docs/10-plan-de-trabajo-poc.md).

## Desarrollo local

Requiere Go 1.24+ (el `go.mod` fija deliberadamente esa versión — ver nota sobre `modernc.org/sqlite` en `docs/02-arquitectura.md` §2.6 antes de actualizar esa dependencia).

```bash
go run ./cmd/nexus
```

Al arrancar, Nexus crea (si no existe) el archivo SQLite indicado por `NEXUS_DB_PATH` y aplica las migraciones pendientes automáticamente — no requiere un paso manual aparte.

Variables de entorno (todas opcionales en ambiente `dev`; `NEXUS_JWT_SECRET` y `NEXUS_SGP_API_KEY` son obligatorias en cualquier otro ambiente):

| Variable | Default (solo `dev`) | Descripción |
|---|---|---|
| `NEXUS_ENV` | `dev` | Ambiente: `dev`, `test`, `prod`. |
| `NEXUS_HTTP_ADDR` | `:8080` | Dirección/puerto del servidor HTTP. |
| `NEXUS_LOG_LEVEL` | `info` | Nivel de log: `debug`, `info`, `warn`, `error`. |
| `NEXUS_DB_PATH` | `nexus.db` | Ruta del archivo SQLite (PoC). |
| `NEXUS_JWT_SECRET` | clave insegura de desarrollo | Clave HS256 para firmar los JWT (ver `docs/06-autenticacion-seguridad.md`). |
| `NEXUS_TOKEN_TTL_SECONDS` | `900` | Duración del JWT emitido por `/api/v1/auth/token`. |
| `NEXUS_SGP_API_KEY` | `sgp-dev-local-key` | API Key del cliente `sgp`, sembrado en SQLite en cada arranque. |
| `NEXUS_JOB_CONCURRENCY` | `5` | Cantidad máxima de jobs asíncronos ejecutándose en paralelo. |

### Probar la autenticación y la integración síncrona de prueba

```bash
# 1. Obtener un token
curl -s -X POST http://localhost:8080/api/v1/auth/token \
  -H "Content-Type: application/json" \
  -d '{"client_id":"sgp","api_key":"sgp-dev-local-key"}'

# 2. Usarlo para invocar la integración de prueba
curl -s -X POST http://localhost:8080/api/v1/integrations/mock-echo/send \
  -H "Authorization: Bearer <access_token>" -H "Content-Type: application/json" \
  -d '{"source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{"hola":"mundo"}}'
```

### Probar el patrón asíncrono (mock-batch-pull / mock-batch-push)

```bash
# 1. Disparar un job (fail_every fuerza algunos ítems fallidos -> PARTIAL)
curl -s -X POST http://localhost:8080/api/v1/integrations/mock-batch-pull/send \
  -H "Authorization: Bearer <access_token>" -H "Content-Type: application/json" \
  -d '{"source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{"total_items":4,"fail_every":2}}'
# -> 202 Accepted con job_id y status_url

# 2. Hacer polling del estado
curl -s http://localhost:8080/api/v1/jobs/<job_id> -H "Authorization: Bearer <access_token>"

# 3. Una vez terminado (COMPLETED/FAILED/PARTIAL), obtener el detalle
curl -s http://localhost:8080/api/v1/jobs/<job_id>/result -H "Authorization: Bearer <access_token>"

# 4. Confirmar que ya se consumió el resultado
curl -s -X POST http://localhost:8080/api/v1/jobs/<job_id>/ack -H "Authorization: Bearer <access_token>"
```

`mock-batch-push` usa el mismo contrato pero simula el modo `delivery_mode=push_db`: no expone `result_url` y en cambio inserta los registros exitosos en una tabla SQLite `sgp_simulated_inbox` que hace las veces de la base de datos de SGP (ver `docs/05-patron-asincrono.md` §5.5).

Comandos útiles:

```bash
go build ./...     # compila todo
go vet ./...        # análisis estático
go test ./...        # pruebas
```
