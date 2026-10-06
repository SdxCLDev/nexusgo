# 1. Visión y Alcance

## 1.1 Problema

SGP necesita intercambiar información con múltiples aplicaciones externas (SAP, PEL, AMD, y otras futuras). Cada una de estas aplicaciones expone sus propios mecanismos de integración (REST, SOAP/WSDL, conexión directa a base de datos), con sus propios esquemas de autenticación, formatos de datos y reglas de negocio de integración.

Sin un punto central, cada equipo que necesita integrar SGP con un sistema externo debe:

- Conocer en detalle el protocolo y la documentación técnica de ese sistema externo.
- Implementar su propia lógica de autenticación, reintentos, manejo de errores y logging.
- Duplicar esfuerzo cuando múltiples equipos integran con el mismo sistema externo de formas distintas.

Esto genera inconsistencia, dificulta la trazabilidad y aumenta el costo de mantenimiento.

## 1.2 Solución: Nexus

Nexus es el **único punto de integración** entre SGP y las aplicaciones externas. Concentra en un solo equipo y una sola base de código:

- El conocimiento de **cómo** hablar con cada sistema externo (REST, SOAP, DB directa, u otro protocolo).
- La lógica de autenticación contra cada sistema externo.
- El formateo/transformación de datos entre el modelo de SGP y el modelo de cada sistema externo.
- El logging y la auditoría de cada operación de integración.

Hacia SGP (y hacia cualquier otro consumidor interno), Nexus **publica únicamente API REST**, con un contrato genérico y consistente, independientemente de cómo se comunique internamente con el sistema externo correspondiente.

## 1.3 Principio de diseño central

> El equipo de SGP solo necesita conocer la documentación de integración de **Nexus**. No necesita conocer SAP, PEL, AMD, ni cómo se accede a ellos.

> El equipo de Nexus es quien lee y analiza la documentación de cada sistema externo, y es responsable de mantener esa integración funcionando.

Esto convierte a Nexus en una capa de **anti-corrupción** (en términos de Domain-Driven Design): aísla a SGP de los detalles, formatos y particularidades de cada sistema externo.

## 1.4 Objetivos

- **O1 — Punto único de integración**: toda comunicación SGP ↔ sistemas externos pasa por Nexus.
- **O2 — Contrato uniforme**: un mismo formato de "sobre" (envelope) de solicitud/respuesta para todas las integraciones, sin importar el protocolo real usado por el sistema externo.
- **O3 — Multi-protocolo de salida**: capacidad de consumir REST, SOAP/WSDL y bases de datos directamente como mecanismos de integración con sistemas externos.
- **O4 — Publicación única REST**: Nexus solo expone API REST hacia sus consumidores, nunca SOAP ni acceso directo a su base de datos.
- **O5 — Soporte síncrono y asíncrono**: integraciones rápidas se resuelven en el mismo request/response; integraciones masivas o lentas se resuelven con un patrón de job asíncrono con consulta de estado.
- **O6 — Trazabilidad completa**: toda operación queda registrada en logs técnicos y en auditoría de negocio, incluyendo el estado de la comunicación con el sistema externo.
- **O7 — Seguridad**: autenticación de los consumidores de Nexus y gestión segura de credenciales hacia los sistemas externos.
- **O8 — Extensibilidad**: agregar una integración nueva no debe requerir cambios estructurales a Nexus, solo la incorporación de un nuevo módulo de integración siguiendo un patrón establecido (ver [Guía para Nueva Integración](09-guia-nueva-integracion.md)).
- **O9 — Despliegue simple**: binario único (característica de Go), sin dependencias de runtime externas, facilitando el despliegue en los entornos de Sodexo.

## 1.5 Alcance

### Dentro de alcance

- Integraciones **salientes** desde SGP hacia sistemas externos (ej. SGP → AMD, envío de datos).
- Integraciones **entrantes** hacia SGP desde sistemas externos, iniciadas por una llamada de SGP a Nexus (ej. AMD → SGP, descarga de minutas).
- Autenticación de consumidores de Nexus (SGP y otros clientes internos autorizados).
- Gestión y almacenamiento seguro de credenciales de Nexus hacia cada sistema externo.
- Logging técnico y auditoría de negocio de cada transacción de integración.
- Procesamiento asíncrono para integraciones de alto volumen o larga duración, con modelo de job y consulta de estado.
- Entrega de resultados asíncronos en dos modalidades: inserción directa en la base de datos de SGP, o disponibilización para que SGP las "retire" (pull).

### Fuera de alcance (en esta primera fase)

- Nexus no implementa lógica de negocio de SGP ni de los sistemas externos; solo transforma, enruta y orquesta llamadas.
- Nexus no reemplaza mecanismos de integración ya existentes que no pasen por este servicio (se espera una migración progresiva, no un corte total inmediato).
- No se define en esta fase un bus de eventos ni mensajería (Kafka, RabbitMQ, etc.); el modelo asíncrono se resuelve con jobs + polling sobre HTTP (ver [Patrón de Integración Asíncrona](05-patron-asincrono.md)). Esto podría evaluarse a futuro como evolución.
- No se define interfaz de usuario (UI) para administración; la administración de integraciones y credenciales se realiza mediante configuración y/o endpoints administrativos protegidos (a definir en detalle durante la implementación).

## 1.6 Glosario

| Término | Definición |
|---|---|
| **Integración** | Unidad de trabajo que conecta un flujo de datos entre SGP y un sistema externo, identificada por un `integration_id` único y estable. |
| **Sistema externo** | Aplicación con la que Nexus se integra: SAP, PEL, AMD, u otras futuras. |
| **Envelope** | Estructura JSON genérica que usa SGP para invocar una integración en Nexus, y que Nexus usa para responder. |
| **Job** | Unidad de trabajo asíncrono con un ciclo de vida propio (pendiente, en progreso, completado, fallido), usada para integraciones largas o masivas. |
| **Adaptador** | Componente interno de Nexus que sabe comunicarse con un protocolo específico de un sistema externo (REST, SOAP, DB). |
| **Correlation ID** | Identificador único de una solicitud de integración, usado para trazabilidad end-to-end entre SGP, Nexus y el sistema externo. |
