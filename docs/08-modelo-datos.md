# 8. Modelo de Datos

Nexus requiere persistencia propia (independiente de SGP y de los sistemas externos) para su catálogo de integraciones, el control de autenticación, los jobs asíncronos y la auditoría. Este documento describe las entidades principales a nivel lógico, independiente del motor de base de datos.

> **Motor de base de datos — fase actual**: durante la prueba de concepto, Nexus usa **SQLite** (archivo embebido, sin servidor externo). La migración futura a **PostgreSQL**, cuando el proyecto madure, está prevista desde el diseño; ver las consideraciones de compatibilidad y mapeo de tipos en [§8.8](#88-nota-sobre-el-motor-de-base-de-datos-sqlite-en-la-poc--postgresql-a-futuro).

## 8.1 Tabla `clients`

Representa a los consumidores autorizados de Nexus (SGP y otros sistemas internos).

| Columna | Tipo | Descripción |
|---|---|---|
| `client_id` | string (PK) | Identificador del cliente, ej. `"sgp"`. |
| `api_key_hash` | string | Hash de la API Key (bcrypt/argon2), nunca el valor en claro. |
| `scopes` | array/json | Lista de `integration_id` que este cliente puede invocar. |
| `status` | enum | `ACTIVE`, `REVOKED`. |
| `created_at` | timestamp | Fecha de alta. |
| `last_used_at` | timestamp (nullable) | Última vez que se emitió un token para este cliente. |

## 8.2 Tabla `integrations` (catálogo)

Metadata de cada integración registrada en el sistema, reflejando lo que el código tiene implementado (puede poblarse al arranque desde el `Registry`, o mantenerse como tabla de configuración que el `Registry` lee).

| Columna | Tipo | Descripción |
|---|---|---|
| `integration_id` | string (PK) | Identificador estable, ej. `"amd-to-sgp-descarga-minuta"`. |
| `name` | string | Nombre descriptivo. |
| `external_system` | string | Sistema externo asociado (`AMD`, `SAP`, `PEL`, ...). |
| `direction` | enum | `OUTBOUND`, `INBOUND`, `BIDIRECTIONAL`. |
| `mode` | enum | `SYNC`, `ASYNC`. |
| `delivery_mode` | enum (nullable) | Para integraciones `ASYNC` con dirección `INBOUND`: `push_db`, `pull_api`. |
| `version` | string | Versión del contrato de esa integración. |
| `status` | enum | `ACTIVE`, `DISABLED` (ej. en mantenimiento). |
| `created_at` / `updated_at` | timestamp | Auditoría de la propia definición. |

## 8.3 Tabla `jobs`

Ciclo de vida de cada ejecución asíncrona (ver [Patrón de Integración Asíncrona](05-patron-asincrono.md)).

| Columna | Tipo | Descripción |
|---|---|---|
| `job_id` | UUID (PK) | Identificador del job. |
| `integration_id` | string (FK → `integrations`) | Integración que originó el job. |
| `correlation_id` | UUID | Enlaza con la solicitud original. |
| `client_id` | string (FK → `clients`) | Cliente que invocó la integración. |
| `status` | enum | `PENDING`, `RUNNING`, `COMPLETED`, `FAILED`, `PARTIAL`. |
| `request_payload` | json | Payload original de la solicitud (redactado si corresponde). |
| `progress_total` | int (nullable) | Total de elementos a procesar, una vez conocido. |
| `progress_processed` | int | Elementos procesados hasta el momento. |
| `progress_failed` | int | Elementos fallidos hasta el momento. |
| `result_summary` | json (nullable) | Resumen del resultado al completar. |
| `delivery_mode` | enum | `push_db`, `pull_api` (heredado de la integración). |
| `acked_at` | timestamp (nullable) | Momento en que SGP confirmó haber consumido el resultado. |
| `created_at` | timestamp | Creación del job. |
| `updated_at` | timestamp | Última actualización de estado/progreso. |
| `finished_at` | timestamp (nullable) | Momento de finalización (cualquier estado terminal). |

### 8.3.1 Tabla `job_items` (detalle, opcional según integración)

Para jobs que procesan múltiples elementos (ej. minutas) y requieren trazabilidad individual, especialmente en caso de resultado `PARTIAL`.

| Columna | Tipo | Descripción |
|---|---|---|
| `job_item_id` | UUID (PK) | Identificador del ítem. |
| `job_id` | UUID (FK → `jobs`) | Job al que pertenece. |
| `external_id` | string | Identificador del elemento en el sistema externo (ej. número de minuta). |
| `status` | enum | `SUCCESS`, `FAILED`. |
| `data` | json (nullable) | Datos transformados, si `status = SUCCESS` y el modo es `pull_api`. |
| `error_detail` | string (nullable) | Detalle del error, si `status = FAILED`. |
| `created_at` | timestamp | Momento de procesamiento del ítem. |

## 8.4 Tabla `audit_log`

Registro de auditoría de negocio (ver [Logging y Auditoría §7.2](07-logging-auditoria.md#72-auditoría-de-negocio)).

| Columna | Tipo | Descripción |
|---|---|---|
| `audit_id` | UUID (PK) | Identificador del registro. |
| `correlation_id` | UUID | Enlaza con la solicitud/job original. |
| `job_id` | UUID (FK → `jobs`, nullable) | Si corresponde a una ejecución asíncrona. |
| `integration_id` | string (FK → `integrations`) | Integración ejecutada. |
| `client_id` | string (FK → `clients`) | Cliente que originó la solicitud. |
| `external_system` | string | Sistema externo involucrado. |
| `direction` | enum | `OUTBOUND`, `INBOUND`. |
| `mode` | enum | `SYNC`, `ASYNC`. |
| `status` | enum | `INICIADO`, `EXITOSO`, `FALLIDO`, `PARCIAL`. |
| `request_summary` | json | Resumen/payload redactado de la solicitud. |
| `response_summary` | json | Resumen/payload redactado de la respuesta. |
| `error_detail` | string (nullable) | Detalle del error, si aplica. |
| `started_at` | timestamp | Inicio de la operación. |
| `finished_at` | timestamp (nullable) | Fin de la operación. |
| `duration_ms` | int (nullable) | Duración total. |

Este registro es append-only (ver [§7.2.4](07-logging-auditoria.md#724-inmutabilidad)): no se actualiza un registro `EXITOSO`/`FALLIDO`/`PARCIAL` ya escrito; un nuevo evento relacionado con el mismo `correlation_id` genera una nueva fila.

## 8.5 Tabla `external_credentials`

Credenciales de Nexus hacia cada sistema externo (ver [Autenticación y Seguridad §6.4](06-autenticacion-seguridad.md#64-almacenamiento-de-credenciales-hacia-sistemas-externos)).

| Columna | Tipo | Descripción |
|---|---|---|
| `external_system` | string (PK) | Sistema externo (`AMD`, `SAP`, `PEL`). |
| `credential_type` | enum | `BASIC`, `OAUTH2`, `API_KEY`, `CERTIFICATE`, etc. |
| `encrypted_payload` | blob/string | Credenciales cifradas (usuario/clave, client secret, etc., según `credential_type`). |
| `environment` | enum | `DEV`, `TEST`, `PROD` — para mantener credenciales separadas por ambiente. |
| `updated_at` | timestamp | Última rotación/actualización. |

## 8.6 Relación entre entidades (resumen)

```
clients ──< jobs >── integrations
                 │
                 ├──< job_items
                 │
audit_log >── integrations
audit_log >── clients
audit_log >── jobs (opcional, cuando aplica)

external_credentials (independiente, referenciada internamente por integration_id/external_system en el código, no por FK estricta)
```

## 8.7 Índices sugeridos

- `jobs(correlation_id)`, `jobs(status, updated_at)` — para polling eficiente y tareas de limpieza de jobs vencidos.
- `audit_log(correlation_id)`, `audit_log(integration_id, started_at)` — para consultas de trazabilidad y reportes por integración/periodo.
- `job_items(job_id, status)` — para listar rápidamente los ítems fallidos de un job `PARTIAL`.

## 8.8 Nota sobre el motor de base de datos: SQLite en la PoC → PostgreSQL a futuro

Para que la migración de **SQLite** (PoC) a **PostgreSQL** (madurez) sea un cambio de infraestructura y no una reescritura, el modelo lógico de este documento se diseñó evitando construcciones exclusivas de un motor. Consideraciones a aplicar durante la implementación:

| Tipo lógico | Implementación en SQLite (PoC) | Implementación futura en PostgreSQL |
|---|---|---|
| `UUID` | `TEXT` (string UUID v4) | `UUID` nativo, o `TEXT` si se prefiere mantener el mismo formato sin cambios. |
| `enum` | `TEXT` + `CHECK (columna IN (...))` | `TEXT`/`VARCHAR` + `CHECK`, o tipo `ENUM` nativo de Postgres si se decide adoptarlo. |
| `json` | `TEXT` (payload serializado), accedido con funciones `json_*` de SQLite cuando se necesite consultar campos internos | `JSONB` nativo. |
| `array` (ej. `scopes`) | `TEXT` con JSON serializado (`["a","b"]`) | `JSONB` o `TEXT[]` nativo. |
| `timestamp` | `TEXT` en formato ISO 8601 UTC (ej. `2026-10-06T14:32:00Z`) | `TIMESTAMPTZ`. |
| `blob` | `BLOB` | `BYTEA`. |
| Autoincremento / PK | `TEXT` (UUID generado en la aplicación, no autoincremental) para todas las PK | Igual criterio — se evita depender de `SERIAL`/`AUTOINCREMENT` desde el día uno, generando los IDs en la aplicación. |

Reglas prácticas para no acoplarse a SQLite:

- **Generar los IDs (UUID) en la capa de aplicación Go**, nunca depender de autoincrementales de SQLite — así el mismo código funciona igual contra PostgreSQL.
- **Evitar SQL específico de SQLite** (ej. `PRAGMA`, funciones `json_extract` en queries de negocio); si se necesita consultar dentro de un campo JSON, preferir resolverlo en la aplicación Go en esta fase, dejando la consulta nativa en JSON/JSONB como optimización al migrar a PostgreSQL.
- **Usar `database/sql` + migraciones versionadas** (ej. `golang-migrate` o similar) escritas, en lo posible, en SQL estándar ANSI; cuando una sentencia difiera entre motores, mantener un archivo de migración por motor bajo el mismo número de versión.
- **Concurrencia**: SQLite serializa escrituras (un solo escritor a la vez). Para la PoC esto es aceptable dado el volumen esperado, pero el Job Manager y el pool de workers (ver [Arquitectura §2.6](02-arquitectura.md#26-decisiones-técnicas-sugeridas)) deben evitar asumir alta concurrencia de escritura mientras se use SQLite — esta limitación desaparece al migrar a PostgreSQL.
- **Un solo archivo de base de datos** (ej. `nexus.db`) es suficiente para la PoC; debe excluirse del control de versiones y respaldarse manualmente mientras no exista un plan de backup formal.

Esta tabla y estas reglas deben revisarse y actualizarse en el momento en que se planifique formalmente la migración a PostgreSQL, incorporando también el plan de migración de datos existentes (export/import o script de migración dedicado).
