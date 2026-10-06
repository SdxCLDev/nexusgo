# 4. Patrón de Integración Síncrona — Caso de referencia: SGP → AMD (envío de datos)

Este documento describe el flujo de referencia para integraciones **síncronas**, salientes desde SGP hacia un sistema externo. Se usa AMD como ejemplo, pero el patrón aplica a cualquier sistema externo (SAP, PEL, etc.) cuando la operación es puntual y de respuesta rápida.

## 4.1 Cuándo usar el patrón síncrono

- La operación involucra **un registro o un lote pequeño** de datos.
- El sistema externo responde en un tiempo acotado (ej. < 10-15 segundos).
- SGP necesita conocer el resultado de inmediato para continuar su propio flujo (ej. marcar un estado, mostrar una confirmación al usuario).

Si la operación es masiva o de duración incierta, usar el [patrón asíncrono](05-patron-asincrono.md) en su lugar.

## 4.2 Flujo end-to-end

```
 SGP                          Nexus                              AMD
  │                             │                                  │
  │ 1. POST /integrations/      │                                  │
  │    sgp-to-amd-envio-minuta/ │                                  │
  │    send  { payload }        │                                  │
  ├────────────────────────────►│                                  │
  │                             │ 2. Valida envelope y payload      │
  │                             │ 3. Registra inicio en audit/log   │
  │                             │ 4. Autentica contra AMD           │
  │                             │    (si el token no está vigente)  │
  │                             ├─────────────────────────────────►│
  │                             │◄─────────────────────────────────┤
  │                             │        token / sesión             │
  │                             │ 5. Mapea payload SGP -> modelo AMD│
  │                             │ 6. Invoca endpoint de envío de AMD│
  │                             ├─────────────────────────────────►│
  │                             │                                   │
  │                             │◄─────────────────────────────────┤
  │                             │   resultado del envío (ok/error) │
  │                             │ 7. Registra resultado en audit/log│
  │ 8. Respuesta { status,      │                                   │
  │    data, correlation_id }   │                                   │
  │◄────────────────────────────┤                                   │
```

## 4.3 Responsabilidades por paso

| Paso | Responsable | Detalle |
|---|---|---|
| 1 | SGP | Construye el envelope genérico (ver [§3.4](03-contrato-api-rest.md#34-envelope-de-solicitud-send)) con el `payload` específico de esta integración y lo envía a Nexus. |
| 2 | Nexus (core) | Valida estructura del envelope y, si la integración define un esquema de `payload`, lo valida también. Rechaza con `400` si falla. |
| 3 | Nexus (audit) | Crea un registro de auditoría con estado `INICIADO`, `correlation_id`, `integration_id`, `source_system` y timestamp. |
| 4 | Nexus (integración AMD) | Verifica si tiene un token vigente para AMD; si no, ejecuta el flujo de autenticación propio de AMD (ver §4.4) y lo cachea según su tiempo de expiración. |
| 5 | Nexus (integración AMD, `mapper.go`) | Transforma el `payload` recibido de SGP al formato exacto que espera el endpoint de AMD (nombres de campo, tipos, codificaciones). |
| 6 | Nexus (integración AMD, `client.go`) | Invoca el endpoint REST de AMD usando `internal/adapters/restclient`, con reintentos acotados en caso de error transitorio (timeout, 5xx). |
| 7 | Nexus (audit) | Actualiza el registro de auditoría con el resultado: `EXITOSO` o `FALLIDO`, incluyendo el código/mensaje devuelto por AMD. |
| 8 | Nexus (API) | Traduce el resultado al formato de respuesta estándar de Nexus y responde a SGP. |

## 4.4 Autenticación de Nexus hacia AMD

Nexus mantiene, por cada sistema externo, sus propias credenciales y lógica de autenticación — completamente opaca para SGP:

- Las credenciales de Nexus hacia AMD se almacenan en `internal/storage/credentialstore`, cifradas en reposo (ver [Autenticación y Seguridad §6.4](06-autenticacion-seguridad.md)).
- El token/sesión obtenido de AMD se cachea en memoria (con expiración) para evitar autenticar en cada solicitud.
- Si AMD invalida el token (ej. respuesta 401), la integración reintenta **una vez** reautenticando antes de reportar error a SGP.

## 4.5 Manejo de errores

| Escenario | Respuesta de Nexus a SGP |
|---|---|
| Payload inválido (falta campo obligatorio) | `400 INVALID_PAYLOAD` con detalle del campo faltante. |
| AMD no responde (timeout) tras agotar reintentos | `502 EXTERNAL_SYSTEM_ERROR` con detalle del timeout. |
| AMD responde rechazando la operación por regla de negocio (ej. duplicado) | `422 BUSINESS_RULE_REJECTED` con el detalle que entregó AMD en `details.external_body`. |
| AMD está caído / integración deshabilitada manualmente | `503 INTEGRATION_UNAVAILABLE`. |
| Error interno de Nexus (bug, excepción no controlada) | `500 INTERNAL_ERROR`, sin exponer detalles internos; se registra el stack trace en logs técnicos. |

## 4.6 Reintentos

- Los reintentos ante fallas **transitorias** (timeout, error de red, 5xx) del sistema externo son responsabilidad de Nexus, no de SGP, y se configuran por integración (ej. 3 intentos con backoff exponencial).
- SGP **no debe** reintentar automáticamente la misma solicitud salvo que reciba `502` o `503`; en ese caso, debe reenviar con el **mismo** `correlation_id` para aprovechar la idempotencia descrita en [§3.9](03-contrato-api-rest.md#39-idempotencia).

## 4.7 Ejemplo de implementación (ilustrativo)

```go
package amd

type EnvioMinutaIntegration struct {
    client *Client
    cfg    Config
}

func (i *EnvioMinutaIntegration) Metadata() core.Metadata {
    return core.Metadata{
        ID:        "sgp-to-amd-envio-minuta",
        Name:      "Envío de minuta a AMD",
        Direction: core.DirectionOutbound,
        Mode:      core.ModeSync,
        Version:   "1.0",
    }
}

func (i *EnvioMinutaIntegration) HandleSend(ctx context.Context, req core.SendRequest) (core.SendResult, error) {
    var in sgpMinutaPayload
    if err := json.Unmarshal(req.Payload, &in); err != nil {
        return core.SendResult{}, core.NewValidationError("payload inválido: %w", err)
    }

    amdPayload := mapToAMD(in)

    token, err := i.client.EnsureToken(ctx)
    if err != nil {
        return core.SendResult{}, core.NewExternalError("no se pudo autenticar en AMD: %w", err)
    }

    resp, err := i.client.EnviarMinuta(ctx, token, amdPayload)
    if err != nil {
        return core.SendResult{}, core.NewExternalError("error al enviar minuta a AMD: %w", err)
    }

    return core.SendResult{
        Status:  "SUCCESS",
        Data:    resp,
        Message: "Minuta enviada correctamente a AMD",
    }, nil
}
```

El núcleo (`internal/core`) se encarga de envolver el resultado o el error en la respuesta HTTP estándar y de registrar auditoría/log, por lo que cada integración solo se preocupa de su propia lógica de negocio hacia el sistema externo.
