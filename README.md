# Nexus

Servicio único de integración entre SGP y aplicaciones externas (SAP, PEL, AMD, etc.), desarrollado en Go.

Documentación de especificación completa en [`docs/`](docs/README.md). Plan de trabajo de la prueba de concepto en [`docs/10-plan-de-trabajo-poc.md`](docs/10-plan-de-trabajo-poc.md).

## Desarrollo local

Requiere Go 1.24+.

```bash
go run ./cmd/nexus
```

Variables de entorno (todas opcionales, con valores por defecto para desarrollo):

| Variable | Default | Descripción |
|---|---|---|
| `NEXUS_ENV` | `dev` | Ambiente: `dev`, `test`, `prod`. |
| `NEXUS_HTTP_ADDR` | `:8080` | Dirección/puerto del servidor HTTP. |
| `NEXUS_LOG_LEVEL` | `info` | Nivel de log: `debug`, `info`, `warn`, `error`. |
| `NEXUS_DB_PATH` | `nexus.db` | Ruta del archivo SQLite (PoC). |

Comandos útiles:

```bash
go build ./...     # compila todo
go vet ./...        # análisis estático
go test ./...        # pruebas
```
