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
| `NEXUS_PUBLIC_BASE_PATH` | *(vacío)* | Prefijo si Nexus se publica detrás de un reverse proxy en un sub-path (ej. `/nexus`). Ver [Despliegue en Linux](#despliegue-en-linux-systemd--nginx-reverse-proxy) más abajo. |

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
- `deploy\env.local.ps1.example`

### 3. Configurar las variables de entorno (una sola vez)

Fuera del ambiente `dev`, `NEXUS_JWT_SECRET` y `NEXUS_SGP_API_KEY` son obligatorias (Nexus no arranca sin ellas — ver `internal/config/config.go`). No requiere permisos de Administrador ni tocar el registro de Windows: solo copiar y completar un archivo.

```powershell
cd C:\Nexus
copy env.local.ps1.example env.local.ps1
notepad env.local.ps1   # reemplazar los valores de ejemplo por los reales
```

`run.ps1` carga `env.local.ps1` automáticamente si existe (fija las variables solo para el proceso de `nexus.exe` que lanza, nada persistente a nivel de sistema) — no hace falta cerrar la sesión ni reiniciar.

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

## Despliegue en Linux (systemd + nginx reverse proxy)

Pensado para un server con nginx ya instalado, publicando Nexus en un sub-path (ej. `https://amddev.sodexhochile.cl/nexus`) sin necesidad de abrir un puerto nuevo en el firewall: Nexus escucha solo en `127.0.0.1`, y nginx lo expone a internet por el puerto que ya esté abierto (443/80).

### 1. Compilar

Cross-compilation desde cualquier SO con Go (no requiere CGO ni un toolchain de C, gracias al driver SQLite en Go puro):

```bash
GOOS=linux GOARCH=amd64 go build -o deploy/nexus ./cmd/nexus
```

El binario resultante es estático y autocontenido (incluye SQLite y las migraciones embebidas).

### 2. Copiar al server

Transferí al server (`scp`, etc.), por ejemplo a `/opt/nexus/`:

- `deploy/nexus` (el binario)
- `deploy/nexus.env.example`
- `deploy/nexus.service`

### 3. Configurar las variables de entorno

```bash
cd /opt/nexus
cp nexus.env.example nexus.env
chmod 600 nexus.env          # contiene secretos
nano nexus.env               # completar NEXUS_JWT_SECRET, NEXUS_SGP_API_KEY, etc.
```

Importante: `NEXUS_HTTP_ADDR=127.0.0.1:8080` (Nexus no debe quedar expuesto directamente) y `NEXUS_PUBLIC_BASE_PATH=/nexus` (debe coincidir con el `location` de nginx del paso 5).

### 4. Instalar como servicio systemd

```bash
sudo useradd --system --no-create-home --shell /usr/sbin/nologin nexus   # si no existe
sudo chown -R nexus:nexus /opt/nexus
sudo cp nexus.service /etc/systemd/system/nexus.service
sudo systemctl daemon-reload
sudo systemctl enable --now nexus
sudo systemctl status nexus
```

`systemctl stop nexus` envía SIGTERM, que Nexus ya maneja con apagado prolijo (cierra el servidor HTTP en curso antes de salir). Logs: `journalctl -u nexus -f`.

### 5. Configurar nginx

`deploy/nexus.nginx.conf` trae el bloque `location /nexus/ { ... }` listo para pegar dentro del `server {}` existente de `amddev.sodexhochile.cl` — compartime ese archivo de sitio cuando lo tengas a mano y lo integro ahí directamente. Después de agregarlo:

```bash
sudo nginx -t && sudo systemctl reload nginx
```

### 6. Verificar

```bash
curl http://127.0.0.1:8080/health          # directo, desde el propio server
curl https://amddev.sodexhochile.cl/nexus/health   # a través de nginx
```

Y desde un navegador, `https://amddev.sodexhochile.cl/nexus/docs` para la documentación interactiva de la API.
