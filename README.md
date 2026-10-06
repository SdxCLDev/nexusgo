# Nexus

Servicio único de integración entre SGP y aplicaciones externas (SAP, PEL, AMD, etc.), desarrollado en Go.

Documentación de especificación completa en [`docs/`](docs/README.md). Plan de trabajo de la prueba de concepto en [`docs/10-plan-de-trabajo-poc.md`](docs/10-plan-de-trabajo-poc.md).

## Desarrollo local

Requiere Go 1.24+.

```bash
go run ./cmd/nexus
```

Variables de entorno (todas opcionales en ambiente `dev`; `NEXUS_JWT_SECRET` y `NEXUS_SGP_API_KEY` son obligatorias en cualquier otro ambiente):

| Variable | Default (solo `dev`) | Descripción |
|---|---|---|
| `NEXUS_ENV` | `dev` | Ambiente: `dev`, `test`, `prod`. |
| `NEXUS_HTTP_ADDR` | `:8080` | Dirección/puerto del servidor HTTP. |
| `NEXUS_LOG_LEVEL` | `info` | Nivel de log: `debug`, `info`, `warn`, `error`. |
| `NEXUS_DB_PATH` | `nexus.db` | Ruta del archivo SQLite (PoC). |
| `NEXUS_JWT_SECRET` | clave insegura de desarrollo | Clave HS256 para firmar los JWT (ver `docs/06-autenticacion-seguridad.md`). |
| `NEXUS_TOKEN_TTL_SECONDS` | `900` | Duración del JWT emitido por `/api/v1/auth/token`. |
| `NEXUS_SGP_API_KEY` | `sgp-dev-local-key` | API Key del cliente `sgp` sembrado en memoria durante la PoC. |

### Probar la autenticación localmente

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

Comandos útiles:

```bash
go build ./...     # compila todo
go vet ./...        # análisis estático
go test ./...        # pruebas
```
