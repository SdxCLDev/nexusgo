# Nexus

Servicio único de integración entre SGP y aplicaciones externas (SAP, PEL, AMD, etc.), desarrollado en Go.

Documentación de especificación completa en [`docs/`](docs/README.md). Plan de trabajo de la prueba de concepto en [`docs/10-plan-de-trabajo-poc.md`](docs/10-plan-de-trabajo-poc.md).

## Desarrollo local

Requiere Go 1.24+ (el `go.mod` fija deliberadamente esa versión — ver nota sobre `modernc.org/sqlite` en `docs/02-arquitectura.md` §2.6 antes de actualizar esa dependencia).

```bash
go run ./cmd/nexus
```

Al arrancar, Nexus crea (si no existe) el archivo SQLite indicado por `NEXUS_DB_PATH` y aplica las migraciones pendientes automáticamente — no requiere un paso manual aparte.

Documentación interactiva de la API (Swagger UI), sin autenticación: **http://localhost:8080/docs** (el spec crudo está en `/openapi.json`). Para probar operaciones protegidas desde ahí, primero generá un token con `/api/v1/auth/token` y pegalo con el botón **Authorize**.

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

## Despliegue en un Windows Server

Por ahora, deliberadamente simple: un ejecutable que se levanta directamente, sin registrarlo como servicio de Windows (eso se evaluará más adelante — ver `docs/10-plan-de-trabajo-poc.md`).

### 1. Compilar el ejecutable

Como Nexus ya se compila en una máquina Windows, alcanza con:

```bash
go build -o nexus.exe ./cmd/nexus
```

Si compilás desde Linux/Mac para un server Windows, hacé cross-compilation:

```bash
GOOS=windows GOARCH=amd64 go build -o nexus.exe ./cmd/nexus
```

El binario resultante es autocontenido (incluye el driver de SQLite y las migraciones embebidas) — no necesita instalar nada más en el server.

### 2. Copiar al server

Copiá al server, en una misma carpeta (ej. `C:\Nexus\`):

- `nexus.exe`
- `deploy\run.ps1`
- `deploy\set-env.ps1`

### 3. Configurar las variables de entorno (una sola vez)

Fuera del ambiente `dev`, `NEXUS_JWT_SECRET` y `NEXUS_SGP_API_KEY` son obligatorias (Nexus no arranca sin ellas — ver `internal/config/config.go`). Como Administrador, en el server:

```powershell
cd C:\Nexus
notepad set-env.ps1   # reemplazar los valores de ejemplo por los reales
.\set-env.ps1
```

`setx /M` deja las variables a nivel de máquina, pero solo las ve una sesión **nueva** — cerrá y volvé a abrir la sesión (o reiniciá) antes del siguiente paso.

### 4. Levantar Nexus

```powershell
cd C:\Nexus
.\run.ps1
```

Esto corre en primer plano y además escribe el log en `nexus.log`, junto al ejecutable. Para dejarlo corriendo sin mantener la sesión de PowerShell abierta, usá `Start-Process` en una ventana aparte o una Tarea Programada ("Ejecutar tanto si el usuario inició sesión como si no") que llame a `run.ps1` al iniciar el sistema.

### 5. Verificar

```powershell
Invoke-RestMethod http://localhost:8080/health
```

Y desde un navegador en el server (o donde tenga acceso a `http://<server>:8080`), abrí **`/docs`** para ver la documentación interactiva de la API y probar los endpoints.

> Si el server está detrás de un firewall, recordá habilitar el puerto configurado en `NEXUS_HTTP_ADDR` (por defecto `8080`) para quien necesite acceder a la API o a `/docs`.
