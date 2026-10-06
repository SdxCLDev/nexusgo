# 7. Logging y Auditoría

Nexus distingue dos registros con propósitos distintos y audiencias distintas:

- **Logging técnico**: orientado a diagnóstico, operación y soporte técnico. Vive en archivos/stdout y, opcionalmente, un agregador de logs.
- **Auditoría de negocio**: orientada a trazabilidad de qué integración se ejecutó, cuándo, con qué datos y qué resultado tuvo. Vive en base de datos, es consultable y tiene valor de evidencia.

## 7.1 Logging técnico

### 7.1.1 Formato

Logging estructurado en JSON (una línea por evento), para facilitar ingestión por herramientas de observabilidad:

```json
{
  "timestamp": "2026-10-06T14:32:00.123Z",
  "level": "INFO",
  "correlation_id": "3f1b2e2a-7c2a-4e4e-9a1a-0e9b7a1e2c33",
  "integration_id": "sgp-to-amd-envio-minuta",
  "component": "integrations.amd",
  "message": "Solicitud enviada a AMD",
  "duration_ms": 842,
  "http_status": 200
}
```

### 7.1.2 Niveles

| Nivel | Uso |
|---|---|
| `DEBUG` | Detalle fino para diagnóstico en desarrollo/soporte (ej. payload completo transformado). Deshabilitado por defecto en producción. |
| `INFO` | Eventos normales del ciclo de vida de una solicitud/job (inicio, fin, resultado). |
| `WARN` | Situaciones recuperables o inusuales (ej. reintento exitoso tras timeout, token reautenticado). |
| `ERROR` | Fallas que impidieron completar una operación (error del sistema externo, error interno). |
| `FATAL` | Errores que impiden que Nexus continúe operando (ej. no se puede conectar a su propia base de datos al arrancar). |

### 7.1.3 Qué se registra siempre

- Toda solicitud HTTP recibida: método, ruta, `client_id`, `correlation_id`, duración, status code de respuesta.
- Toda llamada saliente de Nexus hacia un sistema externo: sistema destino, endpoint, duración, status code o error.
- Cambios de estado de un Job (`PENDING → RUNNING → COMPLETED/FAILED/PARTIAL`).
- Eventos de autenticación: emisión de token, fallos de autenticación, reautenticación contra sistemas externos.
- Panics recuperados por el middleware de recovery, con stack trace.

### 7.1.4 Redacción

Aplica lo definido en [Autenticación y Seguridad §6.6](06-autenticacion-seguridad.md#66-redacción-de-datos-sensibles): ningún log técnico debe contener contraseñas, tokens completos, API Keys, ni campos de negocio marcados como sensibles.

### 7.1.5 Retención

- Logs técnicos: retención corta-media (ej. 30-90 días), suficiente para diagnóstico operacional, gestionada por la plataforma de logs/infraestructura, no por Nexus directamente.

## 7.2 Auditoría de negocio

### 7.2.1 Propósito

Responder, en cualquier momento posterior, a preguntas como:

- ¿Qué integraciones se ejecutaron para un `correlation_id` o un registro de negocio determinado?
- ¿Cuándo se envió tal dato a AMD, y qué respondió AMD?
- ¿Cuántas minutas se descargaron desde AMD en un rango de fechas, y cuántas fallaron?
- ¿Quién (qué sistema cliente) invocó una integración determinada?

### 7.2.2 Modelo de registro de auditoría

Cada invocación de integración (síncrona) o cada job (asíncrono) genera **al menos** un registro de auditoría, con posibilidad de múltiples entradas de detalle para jobs con muchos elementos (ver [Modelo de Datos §8.4](08-modelo-datos.md#84-tabla-audit_log)):

| Campo | Descripción |
|---|---|
| `audit_id` | Identificador único del registro de auditoría. |
| `correlation_id` | Enlaza con la solicitud original de SGP. |
| `integration_id` | Integración ejecutada. |
| `source_system` | Sistema que originó la solicitud (ej. `SGP`). |
| `direction` | `OUTBOUND` / `INBOUND`. |
| `mode` | `SYNC` / `ASYNC`. |
| `status` | `INICIADO`, `EXITOSO`, `FALLIDO`, `PARCIAL`. |
| `request_summary` | Resumen o payload (redactado) de la solicitud. |
| `response_summary` | Resumen o payload (redactado) de la respuesta del sistema externo. |
| `external_system` | Sistema externo involucrado (AMD, SAP, PEL). |
| `started_at` / `finished_at` | Marca de tiempo de inicio y fin. |
| `duration_ms` | Duración total. |
| `error_detail` | Detalle del error, si aplica. |

### 7.2.3 Auditoría de jobs asíncronos

Para integraciones asíncronas, además del registro de auditoría a nivel de job, se recomienda conservar un **detalle por elemento** cuando el resultado es `PARTIAL` (ej. qué minutas específicas fallaron y por qué), de forma de poder dar seguimiento manual sin tener que reprocesar todo el job.

### 7.2.4 Inmutabilidad

- Los registros de auditoría, una vez escritos con estado final (`EXITOSO`/`FALLIDO`/`PARCIAL`), no se modifican ni se eliminan por operación normal del sistema. Cualquier corrección debe registrarse como un nuevo evento, no como edición del original.

### 7.2.5 Retención

- Retención más larga que los logs técnicos, dado su valor como evidencia de negocio (ej. 1-2 años o según política de Sodexo/requisitos de cumplimiento del área correspondiente — a confirmar con el equipo de negocio/legal si aplica regulación específica).

### 7.2.6 Consulta

- Se recomienda exponer, al menos para roles administrativos, un endpoint (o acceso directo a BD vía herramienta interna) para consultar auditoría por `correlation_id`, `integration_id`, rango de fechas y estado. El detalle de este endpoint administrativo se define durante la implementación, dado que no es parte del contrato funcional hacia SGP.

## 7.3 Relación entre `correlation_id`, logs y auditoría

El `correlation_id` es la clave que une:

- El log técnico de la solicitud HTTP entrante.
- Los logs técnicos de las llamadas salientes al sistema externo durante esa solicitud o job.
- El registro de auditoría de negocio correspondiente.

Esto permite, dado un `correlation_id` que SGP reporta como problemático, reconstruir el flujo completo de lo ocurrido dentro de Nexus y hacia el sistema externo.
