# 9. Guía para Incorporar una Nueva Integración

Esta guía es para el **equipo de desarrollo de Nexus** cuando necesita agregar soporte para una nueva integración (ej. un nuevo endpoint de AMD, o un sistema completamente nuevo como SAP o PEL).

## 9.1 Paso 0 — Analizar la documentación del sistema externo

Antes de escribir código:

- Leer la documentación técnica del sistema externo: protocolo (REST/SOAP/DB), endpoints relevantes, formato de autenticación, formato de datos de entrada/salida, códigos de error, límites de tasa (rate limits), comportamiento ante duplicados.
- Identificar si la operación es puntual/rápida (candidata a [patrón síncrono](04-patron-sincrono.md)) o masiva/lenta (candidata a [patrón asíncrono](05-patron-asincrono.md)).
- Identificar si es `OUTBOUND` (SGP envía datos), `INBOUND` (SGP solicita datos) o `BIDIRECTIONAL`.

## 9.2 Paso 1 — Definir el `integration_id` y completar la ficha

Convención de nomenclatura: `<origen>-to-<destino>-<accion-corta>`, en minúsculas y con guiones.

Ejemplos: `sgp-to-amd-envio-minuta`, `amd-to-sgp-descarga-minuta`, `sgp-to-sap-sincroniza-maestros`.

Completar una ficha de integración (plantilla en [§9.6](#96-plantilla-de-ficha-de-integración)) que será la documentación que **SGP** (u otro consumidor) necesita para usarla — sin conocer nada del sistema externo detrás.

## 9.3 Paso 2 — Crear el módulo de integración

```
internal/integrations/<sistema>/
├── <nombre_integracion>.go   # implementa core.Integration (y core.AsyncIntegration si aplica)
├── client.go                  # llamadas al sistema externo (reutiliza internal/adapters/*)
├── mapper.go                  # transforma payload SGP <-> modelo del sistema externo
├── auth.go                    # (si el sistema no comparte auth con otras integraciones del mismo sistema)
└── <nombre_integracion>_test.go
```

Si ya existe un paquete para ese sistema externo (ej. ya hay integraciones con AMD), reutilizar `client.go`/`auth.go` existentes y agregar solo el archivo de la nueva integración.

## 9.4 Paso 3 — Implementar la interfaz `Integration`

- Para integraciones síncronas: implementar `HandleSend` (ver ejemplo en [Patrón Síncrono §4.7](04-patron-sincrono.md#47-ejemplo-de-implementación-ilustrativo)).
- Para integraciones asíncronas: implementar también `Execute` sobre `core.AsyncIntegration`, que el Job Manager invoca en background (ver [Patrón Asíncrono](05-patron-asincrono.md)).
- Definir el esquema de validación del `payload` de entrada (ej. con structs + tags de validación, o un validador JSON Schema si se adopta una librería para eso).
- Mapear los errores del sistema externo a los códigos estándar de Nexus (`EXTERNAL_SYSTEM_ERROR`, `BUSINESS_RULE_REJECTED`, etc. — ver [Contrato API REST §3.8](03-contrato-api-rest.md#38-códigos-de-error)). No inventar códigos de error nuevos sin actualizar el contrato.

## 9.5 Paso 4 — Registrar la integración

Agregar la línea de registro en `cmd/nexus/main.go` (o en el módulo de wiring correspondiente):

```go
reg.Register(nuevosistema.NewMiIntegracion(cfg.NuevoSistema))
```

Agregar también la fila correspondiente en la tabla `integrations` (catálogo) o el mecanismo de sincronización que se defina (ver [Modelo de Datos §8.2](08-modelo-datos.md#82-tabla-integrations-catálogo)), para que aparezca en `GET /integrations`.

## 9.6 Paso 5 — Configurar credenciales y entorno

- Si el sistema externo requiere credenciales nuevas, agregarlas a `external_credentials` (cifradas) para cada ambiente (`DEV`/`TEST`/`PROD`) — ver [Autenticación y Seguridad §6.4](06-autenticacion-seguridad.md#64-almacenamiento-de-credenciales-hacia-sistemas-externos).
- Agregar la configuración de endpoints/timeouts/reintentos del nuevo sistema a `internal/config`.

## 9.7 Paso 6 — Pruebas

- **Pruebas unitarias** del `mapper.go` (transformación de datos) sin dependencias externas.
- **Pruebas de integración** contra un mock/stub del sistema externo (ej. `httptest.Server` para REST, o un stub SOAP) cubriendo: caso exitoso, timeout, error 4xx/5xx del sistema externo, respuesta malformada.
- Para integraciones asíncronas: prueba de un job con elementos mixtos (éxito/falla) verificando que el resultado quede `PARTIAL` correctamente.
- Verificar que los logs y el registro de auditoría generados no contengan datos sensibles sin redactar.

## 9.8 Paso 7 — Actualizar la documentación

- Agregar la ficha de la integración (plantilla abajo) a la documentación de integraciones disponibles para consumidores.
- Si la integración introduce un nuevo código de error no cubierto por el estándar, actualizar [Contrato API REST §3.8](03-contrato-api-rest.md#38-códigos-de-error).

## 9.9 Checklist resumen

- [ ] Documentación del sistema externo leída y entendida por el equipo Nexus.
- [ ] `integration_id` definido siguiendo la convención.
- [ ] Ficha de integración completada (ver plantilla).
- [ ] Módulo creado bajo `internal/integrations/<sistema>/`.
- [ ] Interfaz `Integration` (y `AsyncIntegration` si corresponde) implementada.
- [ ] Mapeo de errores del sistema externo a códigos estándar de Nexus.
- [ ] Integración registrada en el `Registry` y en el catálogo (`GET /integrations`).
- [ ] Credenciales configuradas de forma segura por ambiente.
- [ ] Pruebas unitarias y de integración (incluyendo casos de error) escritas y pasando.
- [ ] Logging y auditoría verificados (sin fuga de datos sensibles).
- [ ] Ficha de integración publicada/compartida con el equipo consumidor (SGP).

## 9.10 Plantilla de ficha de integración

Esta ficha es lo único que un equipo consumidor (ej. SGP) necesita leer para usar la integración — no debe requerir conocer el sistema externo.

```markdown
### Integración: <nombre descriptivo>

- **integration_id**: `<id>`
- **Dirección**: OUTBOUND | INBOUND | BIDIRECTIONAL
- **Modo**: SYNC | ASYNC
- **delivery_mode** (solo si ASYNC + INBOUND): push_db | pull_api
- **Descripción**: qué hace esta integración, en 1-2 líneas.

#### Payload de solicitud (`payload`)

| Campo | Tipo | Obligatorio | Descripción |
|---|---|---|---|
| ... | ... | ... | ... |

#### Respuesta síncrona (`data`) o resultado asíncrono (`items[].data`)

| Campo | Tipo | Descripción |
|---|---|---|
| ... | ... | ... |

#### Errores específicos de esta integración

| code | HTTP status | Significado |
|---|---|---|
| ... | ... | ... |

#### Notas operacionales

- Tiempo típico de respuesta / duración de job.
- Límites conocidos (ej. tamaño máximo de lote).
- Ventanas de mantenimiento conocidas del sistema externo, si aplica.
```
