# 3. Contrato API REST

Este documento define el contrato público que Nexus expone a sus consumidores (SGP y otros sistemas internos). Es el **único** documento que un equipo consumidor necesita leer para integrar con cualquier sistema externo a través de Nexus.

## 3.1 Principios del contrato

- Un **único formato de envelope** para todas las integraciones, sin importar el sistema externo involucrado.
- El consumidor (ej. SGP) identifica la integración mediante un `integration_id` estable, nunca mediante detalles del sistema externo.
- El `payload` dentro del envelope es específico de cada integración y se documenta en la ficha de esa integración (ver plantilla en [Guía para Nueva Integración](09-guia-nueva-integracion.md)), pero el **sobre que lo contiene siempre es el mismo**.
- Todas las respuestas incluyen `correlation_id` para trazabilidad end-to-end.
- Versionado del contrato vía path: `/api/v1/...`.

## 3.2 Base URL y versionado

```
https://<host>/api/v1
```

Cambios incompatibles del contrato (breaking changes) incrementan la versión (`/api/v2`). Cambios aditivos (nuevos campos opcionales) no requieren nueva versión.

## 3.3 Endpoints

| Método | Ruta | Propósito |
|---|---|---|
| `POST` | `/integrations/{integration_id}/send` | Invoca una integración (síncrona o dispara una asíncrona). |
| `GET` | `/jobs/{job_id}` | Consulta el estado de un job asíncrono. |
| `GET` | `/jobs/{job_id}/result` | Obtiene el resultado de un job completado (modalidad *pull*). |
| `POST` | `/jobs/{job_id}/ack` | Confirma que SGP ya consumió el resultado de un job (libera el job para limpieza). |
| `GET` | `/integrations` | Catálogo de integraciones disponibles y su metadata (modo, dirección, versión). |
| `GET` | `/integrations/{integration_id}` | Detalle de una integración específica. |
| `POST` | `/auth/token` | Emite un JWT de corta duración a partir de una API Key (ver [Autenticación](06-autenticacion-seguridad.md)). |
| `GET` | `/health` | Liveness check. |
| `GET` | `/ready` | Readiness check (verifica conexión a BD, etc.). |
| `GET` | `/docs` | Documentación interactiva de la API (Swagger UI), sin autenticación. |
| `GET` | `/openapi.json` | Spec OpenAPI 3.0 crudo que consume `/docs`. |

## 3.4 Envelope de solicitud (`send`)

```http
POST /api/v1/integrations/{integration_id}/send
Authorization: Bearer <jwt>
Content-Type: application/json
```

```json
{
  "correlation_id": "3f1b2e2a-7c2a-4e4e-9a1a-0e9b7a1e2c33",
  "source_system": "SGP",
  "timestamp": "2026-10-06T14:32:00Z",
  "payload": {
    "...": "estructura específica de la integración, documentada en su ficha"
  }
}
```

| Campo | Tipo | Obligatorio | Descripción |
|---|---|---|---|
| `correlation_id` | string (UUID v4) | No (Nexus lo genera si no viene) | Identificador de trazabilidad end-to-end. Si SGP ya maneja uno propio, debe enviarlo para hilar sus propios logs con los de Nexus. |
| `source_system` | string | Sí | Sistema que origina la solicitud (ej. `"SGP"`). Permite a Nexus auditar el origen y soportar múltiples consumidores futuros. |
| `timestamp` | string (RFC 3339) | Sí | Momento en que el consumidor generó la solicitud. |
| `payload` | object | Sí | Datos específicos de la integración. Su esquema se documenta por integración. |

`integration_id` **no** va dentro del cuerpo: viaja en la URL, evitando ambigüedad entre "a qué endpoint le pego" y "qué dice el payload".

## 3.5 Respuesta síncrona

Cuando la integración invocada es de modo `SYNC`, la respuesta llega en el mismo request:

```json
{
  "correlation_id": "3f1b2e2a-7c2a-4e4e-9a1a-0e9b7a1e2c33",
  "integration_id": "sgp-to-amd-envio-minuta",
  "status": "SUCCESS",
  "code": "NEXUS_OK",
  "message": "Datos enviados correctamente a AMD",
  "data": {
    "...": "respuesta específica de la integración, si aplica"
  },
  "timestamp": "2026-10-06T14:32:01Z"
}
```

HTTP status: `200 OK` para `SUCCESS`, `502 Bad Gateway` para errores del sistema externo, `422 Unprocessable Entity` para errores de validación de negocio devueltos por el sistema externo. Ver tabla completa en [§3.8](#38-códigos-de-error).

## 3.6 Respuesta asíncrona (aceptación del job)

Cuando la integración invocada es de modo `ASYNC`, Nexus responde de inmediato aceptando el trabajo:

```json
{
  "correlation_id": "3f1b2e2a-7c2a-4e4e-9a1a-0e9b7a1e2c33",
  "integration_id": "amd-to-sgp-descarga-minuta",
  "job_id": "7b9e6c10-1a2b-4c3d-8e4f-5a6b7c8d9e0f",
  "status": "ACCEPTED",
  "status_url": "/api/v1/jobs/7b9e6c10-1a2b-4c3d-8e4f-5a6b7c8d9e0f",
  "timestamp": "2026-10-06T14:32:00Z"
}
```

HTTP status: `202 Accepted`. Detalle completo del ciclo de vida del job en [Patrón de Integración Asíncrona](05-patron-asincrono.md).

## 3.7 Catálogo de integraciones

```http
GET /api/v1/integrations
```

```json
{
  "integrations": [
    {
      "integration_id": "sgp-to-amd-envio-minuta",
      "name": "Envío de minuta a AMD",
      "direction": "OUTBOUND",
      "mode": "SYNC",
      "version": "1.0",
      "status": "ACTIVE"
    },
    {
      "integration_id": "amd-to-sgp-descarga-minuta",
      "name": "Descarga de minutas desde AMD",
      "direction": "INBOUND",
      "mode": "ASYNC",
      "version": "1.0",
      "status": "ACTIVE"
    }
  ]
}
```

Este catálogo permite a los consumidores (y a herramientas de monitoreo) descubrir qué integraciones existen sin necesidad de leer el código fuente de Nexus.

## 3.8 Códigos de error

Todas las respuestas de error siguen la misma forma:

```json
{
  "correlation_id": "3f1b2e2a-7c2a-4e4e-9a1a-0e9b7a1e2c33",
  "integration_id": "sgp-to-amd-envio-minuta",
  "status": "ERROR",
  "code": "EXTERNAL_SYSTEM_ERROR",
  "message": "AMD rechazó la solicitud: código de minuta duplicado",
  "details": {
    "external_status_code": 409,
    "external_body": "..."
  },
  "timestamp": "2026-10-06T14:32:01Z"
}
```

| HTTP status | `code` | Significado |
|---|---|---|
| 400 | `INVALID_REQUEST` | El cuerpo de la solicitud no es JSON válido o le faltan campos obligatorios, fuera del envelope de integración (ej. `/auth/token`). |
| 400 | `INVALID_ENVELOPE` | El envelope no cumple la estructura o faltan campos obligatorios. |
| 400 | `INVALID_PAYLOAD` | El `payload` no cumple el esquema esperado por la integración. |
| 401 | `UNAUTHORIZED` | Falta autenticación o es inválida. |
| 403 | `FORBIDDEN` | El cliente autenticado no tiene permiso sobre esa integración. |
| 404 | `INTEGRATION_NOT_FOUND` | El `integration_id` no existe en el registro. |
| 404 | `JOB_NOT_FOUND` | El `job_id` no existe o ya fue purgado. |
| 409 | `JOB_NOT_FINISHED` | Se pidió `GET /jobs/{id}/result` pero el job todavía no llegó a un estado terminal (`PENDING`/`RUNNING`). |
| 409 | `DUPLICATE_REQUEST` | Se detectó un `correlation_id` ya procesado (idempotencia, ver §3.9). |
| 422 | `BUSINESS_RULE_REJECTED` | El sistema externo rechazó la solicitud por una regla de negocio propia (ej. dato duplicado). |
| 429 | `RATE_LIMITED` | Se superó el límite de solicitudes configurado para el cliente o la integración. |
| 502 | `EXTERNAL_SYSTEM_ERROR` | Error de comunicación con el sistema externo (timeout, 5xx, conexión rechazada). |
| 503 | `INTEGRATION_UNAVAILABLE` | La integración está deshabilitada temporalmente (ej. mantenimiento del sistema externo). |
| 500 | `INTERNAL_ERROR` | Error inesperado dentro de Nexus. |

## 3.9 Idempotencia

Para evitar duplicar operaciones (ej. reintentos de red desde SGP), Nexus trata `correlation_id` como clave de idempotencia por `integration_id`:

- Si llega una solicitud con un `correlation_id` ya procesado exitosamente para la misma integración, Nexus **no vuelve a ejecutar la integración** y responde con el resultado original almacenado (mismo `status`/`data`, HTTP `200`).
- Si la solicitud original sigue en curso, responde `409 DUPLICATE_REQUEST` indicando que ya existe una solicitud en vuelo con ese `correlation_id`.
- La ventana de deduplicación es configurable (por defecto, ej. 24 horas).

## 3.10 Paginación (catálogo y listados futuros)

Para endpoints que devuelvan colecciones potencialmente grandes, se usa paginación basada en cursor:

```
GET /api/v1/jobs?integration_id=amd-to-sgp-descarga-minuta&cursor=<token>&limit=50
```

```json
{
  "items": [ "..." ],
  "next_cursor": "eyJvZmZzZXQiOjUwfQ==",
  "has_more": true
}
```

## 3.11 Convenciones generales

- Fechas en **RFC 3339 / ISO 8601**, siempre en UTC.
- IDs en **UUID v4**.
- Nombres de campos en `snake_case`.
- Content-Type siempre `application/json; charset=utf-8`.
- Todo endpoint (excepto `/health`) requiere autenticación — ver [Autenticación y Seguridad](06-autenticacion-seguridad.md).
