# Nexus — Documentación de Especificación

Nexus es el servicio único de integración entre SGP y aplicaciones externas (SAP, PEL, AMD, y futuras). Desarrollado en Go, se distribuye como un único ejecutable.

## Índice de documentos

1. [Visión y Alcance](01-vision-y-alcance.md) — Qué es Nexus, problema que resuelve, objetivos y lo que queda fuera de alcance.
2. [Arquitectura](02-arquitectura.md) — Componentes internos, capas, estructura de carpetas Go, patrón de registro de integraciones.
3. [Contrato API REST](03-contrato-api-rest.md) — Envelope genérico, endpoints públicos, códigos de estado, versionado, manejo de errores.
4. [Patrón de Integración Síncrona](04-patron-sincrono.md) — Flujo SGP → AMD (envío de datos) como caso de referencia.
5. [Patrón de Integración Asíncrona](05-patron-asincrono.md) — Flujo AMD → SGP (descarga de minutas) con jobs y polling.
6. [Autenticación y Seguridad](06-autenticacion-seguridad.md) — API Key + JWT, control de acceso por integración, seguridad de credenciales hacia sistemas externos.
7. [Logging y Auditoría](07-logging-auditoria.md) — Qué se registra, niveles de log, modelo de auditoría, retención.
8. [Modelo de Datos](08-modelo-datos.md) — Entidades persistentes: catálogo de integraciones, jobs, auditoría, credenciales.
9. [Guía para Nueva Integración](09-guia-nueva-integracion.md) — Checklist y pasos concretos para que el equipo Nexus incorpore una integración nueva.
10. [Plan de Trabajo — PoC](10-plan-de-trabajo-poc.md) — Hoja de ruta por fases para construir la prueba de concepto, usando AMD y SAP como integraciones de ejemplo.

## Convenciones usadas en estos documentos

- Los ejemplos de integración externa usan **AMD** como caso de referencia (sistema con autenticación por token, endpoints de envío y de consulta de minutas), pero el diseño es agnóstico al sistema externo (SAP, PEL u otro futuro siguen el mismo patrón).
- Todo el código de ejemplo está en Go y es ilustrativo, no definitivo.
- `integration_id` es el identificador estable que usa SGP para indicarle a Nexus qué integración está invocando (ej. `sgp-to-amd-envio-minuta`, `amd-to-sgp-descarga-minuta`).
