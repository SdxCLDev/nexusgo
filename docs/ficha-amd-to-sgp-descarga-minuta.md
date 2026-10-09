# Ficha de Integración: Descarga de minutas desde AMD

> Esta ficha es lo único que un equipo consumidor (SGP) necesita para usar la
> integración. No requiere conocer AMD ni cómo Nexus se comunica con él.
> Plantilla: [Guía para Nueva Integración §9.6](09-guia-nueva-integracion.md#910-plantilla-de-ficha-de-integración).

### Integración: Descarga de minutas desde AMD

- **integration_id**: `amd-to-sgp-descarga-minuta`
- **Dirección**: INBOUND (AMD → SGP)
- **Modo**: ASYNC
- **delivery_mode**: `pull_api` *(fase actual)* — las minutas descargadas quedan
  disponibles en `GET /jobs/{job_id}/result`. La entrega `push_db` (inserción
  directa en la base de datos de SGP) se desarrollará en una fase posterior; ver
  [Plan de Trabajo — Fase 7](10-plan-de-trabajo-poc.md).
- **Descripción**: descarga desde AMD **todas las minutas pendientes**
  (encabezado + detalle por minuta), las valida y las deja disponibles para que
  SGP las revise/consuma.

#### Payload de solicitud (`payload`)

El proceso descarga todas las minutas pendientes: **no recibe filtros**. El
`payload` del envelope va vacío.

```json
{
  "source_system": "SGP",
  "timestamp": "2026-10-09T12:00:00Z",
  "payload": {}
}
```

| Campo | Tipo | Obligatorio | Descripción |
|---|---|---|---|
| (ninguno) | — | — | Esta integración no toma parámetros de entrada. |

#### Flujo de uso (asíncrono)

1. `POST /api/v1/integrations/amd-to-sgp-descarga-minuta/send` → responde `202` con `job_id` y `status_url`.
2. `GET /api/v1/jobs/{job_id}` → polling del estado (`PENDING`/`RUNNING`/`COMPLETED`/`PARTIAL`/`FAILED`) y progreso.
3. `GET /api/v1/jobs/{job_id}/result` → **las minutas descargadas**, una por ítem.
4. (Opcional) `POST /api/v1/jobs/{job_id}/ack` → confirma consumo.

Para descubrir descargas pasadas sin recordar el `job_id`:
`GET /api/v1/jobs?integration_id=amd-to-sgp-descarga-minuta`.

#### Resultado asíncrono (`items[].data`)

Cada ítem del resultado corresponde a una minuta. `external_id` es el `id_minuta` de AMD.

```json
{
  "external_id": "92032",
  "status": "SUCCESS",
  "data": {
    "id_minuta": 92032,
    "encabezado": {
      "id_contrato": 5429,
      "id_minuta": 92032,
      "fechaMinuta": "2026-06-01T00:00:00",
      "ceco": "73110",
      "nombreCeco": "PESQUERA LANDES ISLA ROCUANT F",
      "regimen": "Normal Pesquera Landes",
      "ser_nombre": "Servicio especial N°1",
      "id_regimen": 12017,
      "id_tipo_servicio": 10581
    },
    "detalle": [
      {
        "ceco": "73110",
        "regimen": 12017,
        "servicio": 10581,
        "fecha": "2026-06-07T00:00:00",
        "macroEstructura": 58,
        "id_receta": 14110,
        "cantidad_comensales": 100,
        "ponderaciones": 100,
        "tipo_plato": 1,
        "estructura": 24182,
        "orden": 0
      }
    ]
  }
}
```

| Campo (`data`) | Tipo | Descripción |
|---|---|---|
| `id_minuta` | int | Identificador de la minuta en AMD. |
| `encabezado` | object | Encabezado de la minuta (contrato, ceco, régimen, servicio, fechas, costos). |
| `detalle` | array | Filas de detalle (receta, cantidad de comensales, ponderaciones, fecha, estructura). |

Una minuta que falla al descargarse o al validarse aparece con `status: "FAILED"` y
un campo `error`, sin abortar el resto de la descarga (el job termina `PARTIAL`).

#### Errores específicos de esta integración

No introduce códigos nuevos. Usa los estándar de
[Contrato API REST §3.8](03-contrato-api-rest.md#38-códigos-de-error):

| code | HTTP status | Significado |
|---|---|---|
| `EXTERNAL_SYSTEM_ERROR` | 502 | No se pudo autenticar en AMD o falló la consulta de encabezados (el job queda `FAILED`). |

#### Notas operacionales

- El detalle de las minutas se descarga **en paralelo** con un límite de
  concurrencia configurable (`NEXUS_AMD_DETAIL_CONCURRENCY`, por defecto 10) para
  acelerar la descarga sin saturar AMD.
- **Resiliencia**: una minuta que falla (o incluso un error interno inesperado)
  no detiene el resto de la descarga ni afecta al servicio; queda como ítem
  `FAILED` con su motivo. Si falla el job completo (ej. autenticación), el motivo
  queda en `result_summary.error` del estado del job (`GET /jobs/{job_id}`).
- Validación actual: estructural (id de minuta/receta válidos, detalle no vacío,
  comensales no negativos, fecha presente). Las reglas de negocio específicas de
  SGP se afinarán cuando se definan.
- **Decisión de negocio vigente**: las minutas cuyo detalle trae `id_receta` o
  `cantidad_comensales` **negativos** se **rechazan** (ítem `FAILED`, el job
  queda `PARTIAL`). Observado en el ambiente DEV de AMD: ~36/199 minutas con
  `id_receta` negativo y ~2 con `cantidad_comensales` negativa. Si a futuro se
  determina que esos negativos son válidos o significan algo específico, se
  ajustará `validate` en `internal/integrations/amd/mapper.go`.
- Duración del job: proporcional a la cantidad de minutas pendientes (una consulta
  de detalle por minuta).
