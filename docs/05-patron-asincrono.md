# 5. Patrón de Integración Asíncrona — Caso de referencia: AMD → SGP (descarga de minutas)

Este documento describe el flujo de referencia para integraciones **asíncronas**, usadas cuando el volumen de datos o el tiempo de procesamiento hace inviable resolver la operación en un único request/response.

## 5.1 Cuándo usar el patrón asíncrono

- La operación requiere **múltiples llamadas** al sistema externo (ej. un encabezado + N detalles por encabezado).
- El volumen de datos es alto o variable, y el tiempo de procesamiento no se puede acotar de forma confiable.
- SGP no necesita el resultado en el mismo request; le basta con iniciar el proceso y luego consultar el avance o ser notificado.

## 5.2 Modelo de Job

Toda integración asíncrona se ejecuta como un **Job**, con un ciclo de vida propio, persistido en la base de datos de Nexus (ver [Modelo de Datos §8.3](08-modelo-datos.md#83-tabla-jobs)):

```
PENDING ──► RUNNING ──► COMPLETED
                │
                ├──► FAILED
                │
                └──► PARTIAL   (parte de los datos se procesó con error)
```

| Estado | Significado |
|---|---|
| `PENDING` | El job fue aceptado y encolado, aún no comienza a ejecutarse. |
| `RUNNING` | El job está en ejecución; se actualiza `progress` periódicamente. |
| `COMPLETED` | El job terminó exitosamente; el resultado está disponible. |
| `FAILED` | El job terminó con error no recuperable; no hay resultado utilizable. |
| `PARTIAL` | El job terminó, pero algunos elementos fallaron; el resultado incluye detalle de éxitos/fallos. |

## 5.3 Flujo end-to-end

```
 SGP                          Nexus                              AMD
  │                             │                                  │
  │ 1. POST /integrations/      │                                  │
  │  amd-to-sgp-descarga-minuta/│                                  │
  │  send  { payload: filtros } │                                  │
  ├────────────────────────────►│                                  │
  │                             │ 2. Crea Job (PENDING), encola      │
  │ 3. 202 { job_id,            │                                  │
  │     status_url }            │                                  │
  │◄────────────────────────────┤                                  │
  │                             │ 4. Worker toma el Job (RUNNING)   │
  │                             │ 5. Autentica en AMD                │
  │                             ├─────────────────────────────────►│
  │                             │◄─────────────────────────────────┤
  │                             │  6. Consulta encabezados de minuta│
  │                             ├─────────────────────────────────►│
  │                             │◄─────────────────────────────────┤
  │                             │  7. Por cada encabezado, consulta │
  │                             │     detalle de minuta             │
  │                             ├─────────────────────────────────►│
  │                             │◄─────────────────────────────────┤
  │                             │ 8. Valida datos, acumula resultado│
  │                             │    y actualiza progreso del Job   │
  │                             │                                   │
  │ 9. GET /jobs/{job_id}       │                                   │
  ├────────────────────────────►│                                   │
  │◄────────────────────────────┤ (status: RUNNING, progress)       │
  │          ...                │                                   │
  │                             │ 10. Job completo: COMPLETED        │
  │                             │     Según modo configurado:        │
  │                             │     (a) inserta en BD de SGP, o    │
  │                             │     (b) deja resultado disponible  │
  │                             │         para pull                  │
  │                             │ 11. Registra auditoría final        │
  │                             │                                     │
  │                             │ 12. Actualiza estado de la minuta  │
  │                             │     en AMD (ack de integración)    │
  │                             ├─────────────────────────────────►│
  │                             │◄─────────────────────────────────┤
  │ 13. GET /jobs/{job_id}      │                                   │
  ├────────────────────────────►│                                   │
  │◄────────────────────────────┤ (status: COMPLETED)                │
  │ 14. GET /jobs/{job_id}/     │                                   │
  │      result (si modo pull) │                                   │
  ├────────────────────────────►│                                   │
  │◄────────────────────────────┤ { data }                           │
  │ 15. POST /jobs/{job_id}/ack │                                   │
  ├────────────────────────────►│                                   │
```

## 5.4 Responsabilidades por paso

| Paso | Responsable | Detalle |
|---|---|---|
| 1-2 | SGP → Nexus | SGP envía el envelope con los filtros de la descarga (ej. rango de fechas). Nexus valida y crea el `Job` en estado `PENDING`. |
| 3 | Nexus | Responde `202 Accepted` de inmediato con `job_id` y `status_url`, sin bloquear al llamador (ver [§3.6](03-contrato-api-rest.md#36-respuesta-asíncrona-aceptación-del-job)). |
| 4 | Nexus (Job Manager) | Un worker del pool toma el job y lo marca `RUNNING`. |
| 5 | Nexus (integración AMD) | Autentica contra AMD, igual que en el patrón síncrono. |
| 6 | Nexus → AMD | Consulta el endpoint de **encabezados de minutas** según los filtros del `payload`. |
| 7 | Nexus → AMD | Para **cada encabezado**, consulta el endpoint de **detalle de minuta**. Esto se puede paralelizar con un límite de concurrencia configurable para no saturar a AMD. |
| 8 | Nexus | Valida los datos recibidos (reglas mínimas de consistencia), acumula el resultado y actualiza `progress` (`processed`/`total`) en el registro del Job. |
| 9 | SGP → Nexus | SGP puede sondear (`polling`) el estado del job en cualquier momento mientras procesa otras tareas. |
| 10 | Nexus | Al terminar, decide cómo entregar el resultado según el parámetro `delivery_mode` de la integración (ver §5.5). |
| 11 | Nexus (audit) | Registra en auditoría el resultado final: cantidad de minutas procesadas, exitosas, fallidas, duración total. |
| 12 | Nexus → AMD | Si la integración con AMD lo requiere, notifica a AMD el estado final de la integración por cada minuta (ej. "procesada por SGP"), para que AMD no la vuelva a ofrecer. |
| 13-14 | SGP → Nexus | SGP consulta el estado final y, si el modo es *pull*, obtiene el resultado mediante `/jobs/{job_id}/result`. |
| 15 | SGP → Nexus | SGP confirma (`ack`) que ya consumió el resultado; Nexus puede entonces liberar/archivar el job. |

## 5.5 Modalidades de entrega del resultado (`delivery_mode`)

Cada integración asíncrona se configura con uno de los siguientes modos de entrega, según la necesidad del caso de uso:

### a) `push_db` — Inserción directa en la base de datos de SGP

- Nexus, al completar el procesamiento, inserta/actualiza directamente los registros correspondientes en la base de datos de SGP (usando `internal/adapters/dbclient` con credenciales dedicadas y de alcance mínimo).
- SGP solo necesita sondear el estado del job para saber cuándo los datos ya están disponibles en su propia base de datos, y luego actuar sobre ellos con su lógica habitual.
- Recomendado cuando SGP no tiene (o no quiere exponer) un endpoint propio para recibir los datos, y ambas bases de datos están en una red donde esta conexión es viable y aprobada por el equipo de datos.

### b) `pull_api` — Disponibilización para retiro por parte de SGP

- Nexus deja el resultado disponible en `GET /jobs/{job_id}/result`, en el formato genérico de la integración.
- SGP decide cuándo y cómo consumir esos datos, y aplica su propia lógica de persistencia.
- Recomendado cuando SGP prefiere mantener el control total sobre cómo y cuándo se escriben los datos en su propio modelo, o cuando no es deseable abrir una conexión directa de Nexus hacia la base de datos de SGP.

La elección de modalidad se define por integración (no es necesariamente global) y se documenta en la ficha de esa integración.

## 5.6 Formato de estado de Job

```http
GET /api/v1/jobs/{job_id}
```

```json
{
  "job_id": "7b9e6c10-1a2b-4c3d-8e4f-5a6b7c8d9e0f",
  "integration_id": "amd-to-sgp-descarga-minuta",
  "correlation_id": "3f1b2e2a-7c2a-4e4e-9a1a-0e9b7a1e2c33",
  "status": "RUNNING",
  "progress": {
    "total": 240,
    "processed": 110,
    "failed": 2
  },
  "created_at": "2026-10-06T14:32:00Z",
  "updated_at": "2026-10-06T14:33:45Z",
  "finished_at": null
}
```

Cuando `status` es `COMPLETED`, `FAILED` o `PARTIAL`, se incluye además `finished_at` y, si `delivery_mode` es `pull_api`, un `result_url` apuntando a `/jobs/{job_id}/result`.

## 5.7 Formato de resultado (`pull_api`)

```http
GET /api/v1/jobs/{job_id}/result
```

```json
{
  "job_id": "7b9e6c10-1a2b-4c3d-8e4f-5a6b7c8d9e0f",
  "integration_id": "amd-to-sgp-descarga-minuta",
  "summary": {
    "total": 240,
    "success": 238,
    "failed": 2
  },
  "items": [
    {
      "external_id": "MIN-00123",
      "status": "SUCCESS",
      "data": { "...": "datos de la minuta, en formato ya transformado para SGP" }
    },
    {
      "external_id": "MIN-00456",
      "status": "FAILED",
      "error": "Detalle inconsistente: campo 'monto' fuera de rango"
    }
  ]
}
```

## 5.8 Control de concurrencia y protección del sistema externo

- El Job Manager usa un **worker pool** con tamaño configurable (global y, opcionalmente, por sistema externo) para evitar saturar a AMD/SAP/PEL con demasiadas solicitudes simultáneas.
- Cada integración asíncrona puede definir su propio límite de concurrencia hacia el sistema externo (ej. máximo 5 solicitudes de detalle de minuta en paralelo).
- Se debe aplicar backoff ante respuestas `429`/`503` del sistema externo.

## 5.9 Manejo de fallas parciales

Si durante el procesamiento algunos elementos fallan (ej. un detalle de minuta específico no se pudo obtener o no pasó validación):

- El job **no se marca como `FAILED`** completo; se marca `PARTIAL` al finalizar.
- Cada elemento fallido queda registrado individualmente en el resultado (`items[].status = "FAILED"` con su `error`).
- Se registra el detalle en auditoría para seguimiento manual si es necesario.
- `FAILED` se reserva para cuando el job no pudo ni siquiera completar el paso inicial (ej. no se pudo autenticar en AMD, o la consulta de encabezados falló completamente).

## 5.10 Expiración y limpieza de Jobs

- Los jobs completados (`COMPLETED`/`FAILED`/`PARTIAL`) y su resultado se conservan por un período configurable (ej. 7 días) o hasta recibir el `ack` de SGP, lo que ocurra primero según política.
- Pasado el período de retención, el resultado detallado puede purgarse, conservando únicamente el resumen en auditoría para trazabilidad histórica.
