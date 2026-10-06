# 10. Plan de Trabajo — Prueba de Concepto (PoC)

Este documento define cómo se construye Nexus de forma incremental durante la fase de prueba de concepto, usando **AMD** y **SAP** como integraciones de ejemplo. El plan está pensado para avanzar en capas: primero el esqueleto genérico de Nexus (funciona con una integración de prueba simulada), y recién después se conectan las integraciones reales a medida que se entregue su documentación.

Esto permite **no bloquear el desarrollo** del núcleo mientras se espera la documentación de AMD/SAP, y luego "enchufar" cada integración real siguiendo exactamente el patrón ya definido en [Guía para Nueva Integración](09-guia-nueva-integracion.md).

## 10.1 Principio del plan

> Construir el "tubo" completo (API → núcleo → auditoría/log → respuesta) con una integración ficticia de prueba (`echo`/`mock`) antes de conectar sistemas reales. Así, cuando llegue la documentación de AMD o SAP, el trabajo se reduce a implementar **un módulo de integración** siguiendo un patrón ya probado, no a construir infraestructura nueva.

## 10.2 Estado de entradas pendientes

| Insumo | Estado | Bloquea |
|---|---|---|
| Documentación de integración AMD (endpoints, auth, formatos) | **Pendiente** — la entregará el usuario | Fase 6 y 7 (implementación real de AMD) |
| Documentación de integración SAP (protocolo, endpoints, auth) | **Pendiente** — la entregará el usuario | Fase 8 (implementación real de SAP) |

Mientras no llegue esta documentación, se avanza con las Fases 0 a 5, que son independientes del detalle de cada sistema externo.

Cuando llegue cada documentación, se recomienda completar primero la **ficha de integración** (plantilla en [Guía para Nueva Integración §9.6](09-guia-nueva-integracion.md#96-plantilla-de-ficha-de-integración)) antes de tocar código, para validar que la información esté completa (protocolo, autenticación, endpoints, formatos de entrada/salida, manejo de errores).

## 10.3 Hoja de ruta por fases

```
Fase 0  Bootstrap del proyecto
Fase 1  Esqueleto del núcleo (API + Registry + envelope)
Fase 2  Autenticación de entrada (API Key + JWT)
Fase 3  Integración de prueba (mock) síncrona + auditoría/log
Fase 4  Persistencia (SQLite) de catálogo, clientes y auditoría
Fase 5  Patrón asíncrono (Job Manager) + integración de prueba asíncrona
────────────────────────────────────────────────────────────── (espera documentación)
Fase 6  Integración real: AMD síncrona (ej. envío de datos)
Fase 7  Integración real: AMD asíncrona (ej. descarga de minutas)
Fase 8  Integración real: SAP (según protocolo que indique su documentación)
Fase 9  Credenciales externas cifradas + separación por ambiente
Fase 10 Endurecimiento (rate limiting, idempotencia, catálogo, errores)
Fase 11 Pruebas de integración end-to-end y cierre de PoC
```

---

### Fase 0 — Bootstrap del proyecto

**Objetivo**: tener el repositorio Go inicializado y ejecutable, aunque no haga nada todavía.

- [x] Inicializar repositorio git y `go.mod` (módulo `nexusgo` o el nombre definitivo).
- [x] Crear estructura de carpetas base según [Arquitectura §2.2](02-arquitectura.md#22-estructura-de-carpetas-go) (`cmd/nexus`, `internal/...`).
- [x] Configurar `internal/config`: carga de configuración vía variables de entorno, con valores por defecto razonables para desarrollo local. *(Nota: no se implementó carga desde archivo YAML; se evaluará si realmente hace falta antes de agregarla — por ahora las variables de entorno cubren la PoC.)*
- [x] Configurar `internal/logging` con `log/slog` en formato JSON (ver [Logging y Auditoría §7.1](07-logging-auditoria.md#71-logging-técnico)).
- [x] `cmd/nexus/main.go` levanta un servidor HTTP mínimo con `GET /health` y `GET /ready` (ver [Contrato API REST §3.3](03-contrato-api-rest.md#33-endpoints)).
- [x] Comandos básicos (`go build`, `go vet`, `go test`) documentados en `README.md`. *(Nota: sin Makefile — el entorno de desarrollo es Windows sin `make`; los comandos de Go son suficientes y evitan una dependencia extra.)*

**Criterio de aceptación**: el binario compila, levanta, y `GET /health` responde `200`. ✅ Verificado.

---

### Fase 1 — Esqueleto del núcleo

**Objetivo**: tener el "tubo" de enrutamiento de integraciones funcionando, sin lógica de negocio real todavía.

- [x] Definir en `internal/core`: `Integration`, `Metadata`, `SendRequest`, `SendResult` (ver [Arquitectura §2.3](02-arquitectura.md#23-contrato-interno-interfaz-integration)). *(Nota: `AsyncIntegration` se difiere explícitamente a la Fase 5 — definirla ahora acoplaría `core` con `core/jobmanager` en un ciclo de importación; se resolverá al diseñar el Job Manager.)*
- [x] Definir el envelope genérico (`internal/core/envelope.go`) según [Contrato API REST §3.4](03-contrato-api-rest.md#34-envelope-de-solicitud-send) y su validación básica (campos obligatorios).
- [x] Implementar `core.Registry` (alta/búsqueda de integraciones por `integration_id`).
- [x] Implementar handler `POST /integrations/{integration_id}/send` que: valida envelope → busca en `Registry` → si no existe, `404 INTEGRATION_NOT_FOUND` → si existe y es `SYNC`, invoca `HandleSend` y devuelve el resultado mapeado al formato estándar. Las integraciones `ASYNC` responden `503 INTEGRATION_UNAVAILABLE` hasta la Fase 5.
- [x] Implementar handler `GET /integrations` (catálogo, en memoria por ahora, reflejando lo registrado).
- [x] Middlewares base: recovery (captura panics) y logging de requests.
- [x] Integración `mock-echo` (`internal/integrations/mock`) y pruebas automatizadas del router (`internal/api/router_test.go`).

**Criterio de aceptación**: se puede registrar una integración de prueba y probarla end-to-end (sin DB, sin auth todavía). ✅ Verificado con `curl` y pruebas automatizadas.

---

### Fase 2 — Autenticación de entrada

**Objetivo**: proteger los endpoints funcionales con el esquema API Key + JWT.

- [x] Modelar `clients` en memoria (`internal/auth.ClientStore`/`InMemoryClientStore`, un cliente `sgp` sembrado al arrancar); se migra a SQLite en la Fase 4 sin cambiar la interfaz.
- [x] Implementar `POST /auth/token`: valida `client_id` + `api_key`, emite JWT firmado (HS256 para la PoC — ver nota de simplificación abajo) con `scopes` (ver [Autenticación y Seguridad §6.1](06-autenticacion-seguridad.md#61-autenticación-de-entrada-api-key--jwt)).
- [x] Middleware de autenticación (`internal/api/middleware.Authenticate`): valida `Authorization: Bearer <jwt>` y deja los claims en el contexto; el chequeo de `scope` por `integration_id` se hace en el handler `Send` (el patrón de ruteo de `net/http` no expone las variables de ruta a un middleware que envuelve el mux completo).
- [x] Middleware aplicado a los endpoints funcionales (`send`, `catalog`); `/health`, `/ready` y `/auth/token` quedan sin autenticación.

> **Simplificación aceptada para la PoC**: usar HS256 (clave simétrica compartida vía variable de entorno) en lugar de RS256 es aceptable mientras Nexus es un único proceso. Si en el futuro se separan componentes (ej. un validador de tokens independiente), migrar a RS256. Dejar esto registrado como decisión técnica de la PoC, no del diseño final.
>
> **Segunda simplificación aceptada**: el hash de API Keys usa SHA-256 en vez de bcrypt/argon2 (mencionados como referencia en [Autenticación y Seguridad §6.1.5](06-autenticacion-seguridad.md#615-gestión-de-api-keys)). Es aceptable porque las API Keys de Nexus son generadas aleatoriamente con alta entropía (`auth.GenerateAPIKey`, 32 bytes) — a diferencia de una contraseña elegida por una persona, no hace falta un hash lento para resistir fuerza bruta. Evita además sumar una dependencia externa (bcrypt/argon2 no están en la librería estándar de Go).

**Criterio de aceptación**: sin token válido, cualquier endpoint funcional responde `401`; con token y `scope` correcto, responde normalmente; con `scope` incorrecto, `403`. ✅ Verificado con pruebas automatizadas (`router_test.go`) y manualmente con `curl`.

---

### Fase 3 — Integración de prueba (mock) síncrona + auditoría/log

**Objetivo**: validar el flujo completo descrito en [Patrón de Integración Síncrona](04-patron-sincrono.md) sin depender de AMD/SAP reales.

- [x] Ampliar `internal/integrations/mock` (`mock-echo`, `SYNC`/`OUTBOUND`) para simular latencia configurable (`payload.delay_ms`, cancelable por contexto) y forzar resultados (`payload.force`: `success`, `business_error`, `external_error`, `panic`) — cubre los caminos 200/422/502/500 de [Contrato API REST §3.8](03-contrato-api-rest.md#38-códigos-de-error) sin un sistema externo real.
- [x] Implementar `internal/audit`: registro de auditoría en memoria (`InMemoryStore`), con los campos de [Modelo de Datos §8.4](08-modelo-datos.md#84-tabla-audit_log); `Finish` rechaza modificar un registro que ya tiene estado final, aplicando la inmutabilidad de [§7.2.4](07-logging-auditoria.md#724-inmutabilidad).
- [x] Conectar el handler `Send` para que cada invocación genere su entrada de auditoría (`INICIADO` → `EXITOSO`/`FALLIDO`/`PARCIAL`) correlacionada por `correlation_id`.
- [x] Implementar idempotencia por `correlation_id` (`internal/core/idempotency`, ver [Contrato API REST §3.9](03-contrato-api-rest.md#39-idempotencia)): una solicitud en curso responde `409 DUPLICATE_REQUEST`; una ya completada con éxito devuelve el resultado cacheado sin reejecutar; una que terminó en error libera la clave para permitir reintento (alineado con el reintento documentado en [Patrón Síncrono §4.6](04-patron-sincrono.md#46-reintentos)).

> **Hallazgo corregido durante la implementación**: al probar manualmente el caso `force:"panic"` se detectó que un panic dentro de `HandleSend` escapaba hasta el middleware `Recover` (Fase 0), que no conoce `correlation_id`/`integration_id` ni libera la clave de idempotencia ni cierra el registro de auditoría — dejándolo `INICIADO` para siempre y bloqueando cualquier reintento con ese `correlation_id`. Se corrigió envolviendo la llamada a `HandleSend` en `callIntegration` (`internal/api/handlers/send.go`), que recupera el panic localmente y lo convierte en `core.NewInternalError`, permitiendo que siga el mismo camino de limpieza (idempotencia + auditoría + respuesta estándar) que cualquier otro error. Cubierto por `TestSend_PanicIsRecoveredAndDoesNotBlockRetry`.

**Criterio de aceptación**: invocar `mock-echo` genera respuesta correcta, entrada de auditoría y logs correlacionados por `correlation_id`; reenviar el mismo `correlation_id` no duplica la ejecución. ✅ Verificado con pruebas automatizadas (18 casos) y manualmente con `curl`, incluyendo el caso límite del panic.

---

### Fase 4 — Persistencia con SQLite

**Objetivo**: reemplazar las estructuras en memoria de las fases anteriores por persistencia real, usando SQLite (ver [Modelo de Datos §8.8](08-modelo-datos.md#88-nota-sobre-el-motor-de-base-de-datos-sqlite-en-la-poc--postgresql-a-futuro)).

- [x] Elegir y configurar driver SQLite para Go: `modernc.org/sqlite` (Go puro, sin CGO) — ver la nota de driver en [Arquitectura §2.6](02-arquitectura.md#26-decisiones-técnicas-sugeridas). Confirmado necesario: el entorno de desarrollo de esta PoC no tiene compilador de C instalado, por lo que `mattn/go-sqlite3` (CGO) no habría compilado.
- [x] Migraciones versionadas con un runner mínimo hecho a mano (`internal/storage/sqlite/sqlite.go`), en vez de `golang-migrate` — ver nota abajo. Migraciones iniciales para `clients`, `integrations`, `audit_log` en `internal/storage/sqlite/migrations/*.sql` (las de `jobs`/`job_items` se agregan en la Fase 5).
- [x] `internal/storage/sqlite.ClientStore` implementa `auth.ClientStore`; reemplaza a `auth.InMemoryClientStore` en `main.go` (que se mantiene como implementación válida para pruebas rápidas del paquete `internal/api`).
- [x] `internal/storage/sqlite.CatalogStore` implementa la nueva interfaz `core.CatalogStore`; el handler `GET /integrations` ahora lee de ahí (status persistido) en vez de leer directo del `Registry` en memoria. `*core.Registry` también implementa `core.CatalogStore` (todo "ACTIVE"), útil como catálogo de referencia y en pruebas sin base de datos.
- [x] `internal/storage/sqlite.AuditStore` implementa `audit.Store`; reemplaza a `audit.InMemoryStore` en `main.go` (que también se mantiene para pruebas). La inmutabilidad de [§7.2.4](07-logging-auditoria.md#724-inmutabilidad) se refuerza a nivel SQL (`UPDATE ... WHERE finished_at IS NULL`).
- [x] Reglas de compatibilidad futura con PostgreSQL aplicadas: UUIDs generados en Go (`core.NewID`), timestamps en TEXT/ISO 8601 UTC, JSON serializado como TEXT, sin `PRAGMA` ni funciones específicas de SQLite en las consultas de negocio (los `PRAGMA` de configuración de conexión —`busy_timeout`, `journal_mode`, `foreign_keys`— viven solo en el DSN de `sqlite.Open`, no en queries).

> **Simplificación aceptada para la PoC**: en vez de `golang-migrate` (sugerido originalmente), se implementó un runner de migraciones propio de ~80 líneas (`internal/storage/sqlite/sqlite.go`): lee los `.sql` embebidos con `go:embed`, los aplica en orden dentro de una transacción y registra cada uno en `schema_migrations`. Para migraciones puramente DDL como las de esta PoC es suficiente y evita sumar una dependencia más con su propio CLI/convenciones. Si las migraciones crecen en complejidad (datos, rollbacks condicionales), reevaluar `golang-migrate` en una fase posterior.
>
> **Nota sobre la ubicación de `migrations/`**: terminaron en `internal/storage/sqlite/migrations/` y no en un `migrations/` a nivel de repositorio como sugería originalmente [Arquitectura §2.2](02-arquitectura.md#22-estructura-de-carpetas-go), porque `go:embed` no admite rutas que suban de directorio. Documento de arquitectura actualizado para reflejarlo.
>
> **Nota sobre el `go.mod`**: `modernc.org/sqlite` en su versión más reciente exige Go ≥ 1.26; se fijaron versiones algo más antiguas de `modernc.org/sqlite`/`modernc.org/libc`/`golang.org/x/sys` para quedarse en Go 1.24 (la versión ya instalada y documentada en `README.md`) sin forzar una migración de toolchain como efecto secundario de agregar persistencia. Ver detalle en [Arquitectura §2.6](02-arquitectura.md#26-decisiones-técnicas-sugeridas).
>
> **Pendiente, fuera de alcance de esta fase**: `internal/core/idempotency` (Fase 3) sigue en memoria — no hay tabla de idempotencia en [Modelo de Datos](08-modelo-datos.md), por lo que no estaba en el checklist de esta fase. Implica que un reinicio del proceso durante la ventana de deduplicación permitiría reprocesar un `correlation_id` que debería haberse cacheado. Evaluar si esto necesita persistirse en una fase posterior (candidato natural: Fase 9 o 10).

**Criterio de aceptación**: al reiniciar el proceso, clientes, catálogo y auditoría persisten (no se pierden); los datos son consultables directamente en el archivo `.db` con cualquier cliente SQLite. ✅ Verificado con pruebas automatizadas (`internal/storage/sqlite/sqlite_test.go`) y manualmente: se levantó el proceso, se sembró un cliente y se ejecutó una integración, se reinició el proceso contra el mismo archivo `.db`, y tanto el cliente como el catálogo y el registro de auditoría siguieron disponibles.

---

### Fase 5 — Patrón asíncrono (Job Manager) + integración de prueba asíncrona

**Objetivo**: validar el flujo completo descrito en [Patrón de Integración Asíncrona](05-patron-asincrono.md), otra vez sin depender de AMD/SAP reales.

- [ ] Migraciones para `jobs` y `job_items` (ver [Modelo de Datos §8.3](08-modelo-datos.md#83-tabla-jobs)).
- [ ] Implementar `internal/core/jobmanager`: creación de job (`PENDING`), worker pool con tamaño configurable, transición de estados (`RUNNING` → `COMPLETED`/`FAILED`/`PARTIAL`), actualización de `progress`.
- [ ] Implementar handlers `GET /jobs/{job_id}`, `GET /jobs/{job_id}/result`, `POST /jobs/{job_id}/ack` (ver [Contrato API REST §3.6](03-contrato-api-rest.md#36-respuesta-asíncrona-aceptación-del-job)).
- [ ] Crear `internal/integrations/mock` — variante asíncrona (`mock-batch`) que simula una descarga masiva: genera N ítems ficticios, con latencia simulada por ítem y un porcentaje configurable de fallos, para forzar el resultado `PARTIAL` y validar `job_items`.
- [ ] Implementar ambas modalidades de entrega de forma simulada: `pull_api` (ya cubierto por `/jobs/{id}/result`) y `push_db` (para la PoC, simular con una segunda tabla SQLite "externa" que haga las veces de la base de datos de SGP, ya que aún no hay acceso real a SGP).

**Criterio de aceptación**: se puede disparar `mock-batch`, hacer polling de su progreso, verlo terminar `PARTIAL` con ítems de detalle, y obtener el resultado vía `pull_api`; el modo `push_db` deja los datos en la tabla simulada.

---

### Fase 6 — Integración real: AMD (síncrona)

**Bloqueada hasta recibir la documentación de AMD.** Al recibirla:

- [ ] Completar la ficha de integración de AMD (plantilla en [§9.6](09-guia-nueva-integracion.md#96-plantilla-de-ficha-de-integración)) para el flujo síncrono (ej. envío de datos SGP → AMD).
- [ ] Confirmar protocolo real (REST/SOAP) y, si es necesario, completar el adaptador genérico correspondiente en `internal/adapters` (`restclient` ya cubre REST; si AMD expone SOAP, construir/ajustar `soapclient` en esta fase).
- [ ] Implementar `internal/integrations/amd` siguiendo el patrón de [Guía para Nueva Integración §9.3-9.4](09-guia-nueva-integracion.md#93-paso-2--crear-el-módulo-de-integración): `client.go` (auth + llamadas), `mapper.go` (transformación de payload), integración con `core.Integration`.
- [ ] Registrar `sgp-to-amd-<accion>` en el `Registry` y en el catálogo.
- [ ] Credenciales de AMD: por ahora en `external_credentials` sin cifrado fuerte (placeholder), formalizar cifrado real en Fase 9.
- [ ] Pruebas contra un stub HTTP (`httptest.Server`) que simule las respuestas de AMD documentadas, cubriendo éxito, error de negocio y timeout.

**Criterio de aceptación**: `mock-echo` se reemplaza conceptualmente por la integración real de AMD, pasando las mismas pruebas de flujo (éxito, error, auditoría) pero contra el stub de AMD.

---

### Fase 7 — Integración real: AMD (asíncrona — descarga de minutas)

**Bloqueada hasta recibir la documentación de AMD** (puede entregarse junto con la de Fase 6 o por separado).

- [ ] Completar ficha de integración para `amd-to-sgp-descarga-minuta` (o el nombre real que corresponda), confirmando: endpoint de autenticación, endpoint de encabezados, endpoint de detalle, endpoint de actualización de estado (ver flujo de referencia en [Patrón Asíncrono §5.3](05-patron-asincrono.md#53-flujo-end-to-end)).
- [ ] Implementar `Execute` sobre `core.AsyncIntegration` para esta integración: autenticación, consulta de encabezados, consulta de detalle por encabezado (con límite de concurrencia, ver [§5.8](05-patron-asincrono.md#58-control-de-concurrencia-y-protección-del-sistema-externo)), validación, actualización de estado en AMD al finalizar cada minuta.
- [ ] Definir y confirmar con el equipo de SGP el `delivery_mode` real a usar para esta integración (`push_db` o `pull_api`) — reemplaza el mecanismo simulado de la Fase 5 por el real.
- [ ] Pruebas con stub de AMD simulando: lote completo exitoso, lote con fallas parciales, falla total de autenticación.

**Criterio de aceptación**: el flujo replica exactamente el descrito en [Patrón Asíncrono §5.3](05-patron-asincrono.md#53-flujo-end-to-end), con datos reales/stub de AMD en lugar de datos simulados de `mock-batch`.

---

### Fase 8 — Integración real: SAP

**Bloqueada hasta recibir la documentación de SAP.**

- [ ] Completar ficha(s) de integración de SAP, identificando primero el/los protocolo(s) reales que expone (REST, SOAP/WSDL, o conexión directa a base de datos — SAP suele requerir alguno de estos tres, a confirmar con la documentación entregada).
- [ ] Si SAP expone SOAP/WSDL y aún no se construyó `internal/adapters/soapclient` en una fase anterior, construirlo en este punto (cliente SOAP genérico, manejo de `Fault`, WS-Security si aplica).
- [ ] Si SAP requiere conexión directa a base de datos, construir `internal/adapters/dbclient` genérico (ver [Arquitectura §2.5](02-arquitectura.md#25-capa-de-adaptadores-reutilizables)) con pool de conexiones y timeouts, y definir credenciales de acceso de solo lectura/escritura mínima necesaria.
- [ ] Implementar `internal/integrations/sap` siguiendo el mismo patrón que AMD (Fases 6/7), determinando si la(s) integración(es) son síncronas, asíncronas, o ambas.
- [ ] Pruebas con stub/mock del protocolo correspondiente.

**Criterio de aceptación**: al menos una integración SAP (síncrona o asíncrona, según lo que indique su documentación) funcionando end-to-end contra un stub, con auditoría y logging correctos.

---

### Fase 9 — Credenciales externas cifradas y separación por ambiente

**Objetivo**: cerrar la brecha de seguridad dejada como placeholder en las Fases 6-8.

- [ ] Implementar cifrado real en `internal/storage/credentialstore` (ver [Autenticación y Seguridad §6.4](06-autenticacion-seguridad.md#64-almacenamiento-de-credenciales-hacia-sistemas-externos)), con clave de cifrado inyectada vía variable de entorno (no commiteada).
- [ ] Separar configuración de credenciales/endpoints por ambiente (`DEV`/`TEST`/`PROD`) en `external_credentials`.
- [ ] Revisar que ningún log o entrada de auditoría generada en las fases anteriores filtre credenciales o datos sensibles sin redactar (ver [§6.6](06-autenticacion-seguridad.md#66-redacción-de-datos-sensibles)).

**Criterio de aceptación**: las credenciales de AMD/SAP no existen en texto plano en ningún archivo de configuración ni en la base de datos; cambiar de ambiente no requiere recompilar.

---

### Fase 10 — Endurecimiento

**Objetivo**: cubrir los aspectos operacionales que quedaron simplificados durante el desarrollo incremental.

- [ ] Rate limiting por `client_id` (y opcionalmente por integración).
- [ ] Revisión completa de códigos de error ([Contrato API REST §3.8](03-contrato-api-rest.md#38-códigos-de-error)) — confirmar que todas las integraciones reales mapean sus errores correctamente, sin usar `500` como comodín.
- [ ] Expiración/limpieza de jobs vencidos (ver [§5.10](05-patron-asincrono.md#510-expiración-y-limpieza-de-jobs)).
- [ ] Revisión de índices en SQLite (ver [§8.7](08-modelo-datos.md#87-índices-sugeridos)).
- [ ] Documentar en cada ficha de integración (AMD, SAP) las notas operacionales reales observadas (tiempos de respuesta, límites, ventanas de mantenimiento).

---

### Fase 11 — Pruebas end-to-end y cierre de PoC

**Objetivo**: validar la PoC como un todo y dejar una recomendación clara para la siguiente etapa del proyecto.

- [ ] Prueba end-to-end manual (o automatizada) de los tres flujos de referencia: síncrono AMD, asíncrono AMD, y el/los flujo(s) SAP.
- [ ] Revisión de auditoría: verificar que se puede reconstruir el historial completo de una operación a partir de un `correlation_id`.
- [ ] Documento de cierre de PoC (puede ser un nuevo documento `11-resultados-poc.md` cuando corresponda) con: qué funcionó, qué limitaciones se encontraron (especialmente las relacionadas con SQLite, ver [§8.8](08-modelo-datos.md#88-nota-sobre-el-motor-de-base-de-datos-sqlite-en-la-poc--postgresql-a-futuro)), y una recomendación concreta sobre el momento y plan de migración a PostgreSQL.

---

## 10.4 Qué se necesita del usuario para avanzar en Fases 6-8

Para no detener el avance, apenas se entregue la documentación de AMD y/o SAP, idealmente debe incluir (o se completará junto con el usuario si falta algo):

- Protocolo de integración (REST/SOAP/DB) y especificación técnica (OpenAPI/Swagger, WSDL, o descripción de esquema de base de datos, según corresponda).
- Mecanismo de autenticación (usuario/clave, OAuth2, token propio, certificado).
- Endpoints relevantes para cada operación (envío de datos, consulta de encabezados/detalle, actualización de estado, etc.).
- Formato de los datos de entrada/salida de cada endpoint.
- Comportamiento documentado ante errores, duplicados y límites de tasa (rate limits).
- Accesos/credenciales de un ambiente de pruebas (DEV/TEST) del sistema externo, si existen, para poder validar contra el sistema real y no solo contra un stub.

Esta información se vuelca directamente en la ficha de integración correspondiente (plantilla en [§9.6](09-guia-nueva-integracion.md#96-plantilla-de-ficha-de-integración)) antes de comenzar la implementación de cada fase bloqueada.
