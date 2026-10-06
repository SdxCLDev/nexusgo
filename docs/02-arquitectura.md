# 2. Arquitectura

## 2.1 Vista general

Nexus se organiza en capas, siguiendo un estilo hexagonal (puertos y adaptadores), para aislar el núcleo (enrutamiento de integraciones, auditoría, autenticación) de los detalles de cada protocolo externo y de cada sistema con el que se integra.

```
                        ┌───────────────────────────────────────────┐
                        │                 Consumidores               │
                        │         SGP, otros sistemas internos       │
                        └───────────────────┬─────────────────────────┘
                                             │ HTTPS / REST (único protocolo publicado)
                        ┌───────────────────▼─────────────────────────┐
                        │              Capa API (internal/api)        │
                        │  Router, middlewares: auth, logging,        │
                        │  recovery, rate limiting, validación        │
                        └───────────────────┬─────────────────────────┘
                                             │
                        ┌───────────────────▼─────────────────────────┐
                        │            Núcleo (internal/core)           │
                        │  Registro de integraciones, Job Manager,    │
                        │  validación de envelope, orquestación       │
                        └──────┬─────────────┬─────────────┬──────────┘
                               │             │             │
                   ┌───────────▼───┐ ┌───────▼───────┐ ┌───▼────────────┐
                   │ internal/      │ │ internal/      │ │ internal/       │
                   │ integrations/  │ │ integrations/  │ │ integrations/   │
                   │ amd            │ │ sap            │ │ pel             │
                   └───────┬────────┘ └───────┬────────┘ └───────┬─────────┘
                           │                  │                  │
                   ┌───────▼──────────────────▼──────────────────▼─────────┐
                   │          internal/adapters (clientes reutilizables)    │
                   │   RestClient · SoapClient · DbClient                   │
                   └───────┬──────────────────┬──────────────────┬─────────┘
                           │                  │                  │
                      ┌────▼────┐       ┌─────▼─────┐      ┌─────▼─────┐
                      │   AMD   │       │    SAP    │      │    PEL    │
                      │ (REST)  │       │  (SOAP)   │      │ (DB/REST) │
                      └─────────┘       └───────────┘      └───────────┘
```

Transversal a todas las capas: `internal/audit` (auditoría de negocio) y `internal/logging` (logging técnico), invocados desde los middlewares de la API y desde cada integración.

## 2.2 Estructura de carpetas (Go)

```
nexusgo/
├── cmd/
│   └── nexus/
│       └── main.go                  # entrypoint, wiring de dependencias
├── internal/
│   ├── api/
│   │   ├── router.go                 # definición de rutas HTTP
│   │   ├── handlers/
│   │   │   ├── send.go               # POST /integrations/{id}/send
│   │   │   ├── jobs.go               # GET /jobs/{id}, /jobs/{id}/result
│   │   │   ├── catalog.go            # GET /integrations (catálogo)
│   │   │   └── health.go             # GET /health, /ready
│   │   └── middleware/
│   │       ├── auth.go
│   │       ├── logging.go
│   │       ├── recover.go
│   │       └── ratelimit.go
│   ├── core/
│   │   ├── registry.go               # registro de integraciones disponibles
│   │   ├── integration.go            # interfaz Integration (contrato)
│   │   ├── envelope.go               # structs del envelope genérico
│   │   └── jobmanager/
│   │       ├── manager.go            # ciclo de vida de jobs asíncronos
│   │       └── worker_pool.go        # pool de workers para procesar jobs
│   ├── integrations/
│   │   ├── amd/
│   │   │   ├── amd.go                 # implementa core.Integration
│   │   │   ├── client.go              # llamadas REST específicas de AMD
│   │   │   ├── mapper.go              # transforma payload SGP <-> modelo AMD
│   │   │   └── amd_test.go
│   │   ├── sap/
│   │   └── pel/
│   ├── adapters/
│   │   ├── restclient/
│   │   ├── soapclient/
│   │   └── dbclient/
│   ├── auth/
│   │   ├── apikey.go
│   │   └── jwt.go
│   ├── audit/
│   │   └── audit.go                  # escritura de auditoría de negocio
│   ├── logging/
│   │   └── logger.go                 # logger estructurado (ej. slog/zap)
│   ├── storage/
│   │   └── sqlite/
│   │       ├── sqlite.go               # Open() + runner de migraciones embebidas
│   │       ├── clientstore.go          # auth.ClientStore sobre SQLite
│   │       ├── catalogstore.go         # core.CatalogStore sobre SQLite
│   │       ├── auditstore.go           # audit.Store sobre SQLite
│   │       └── migrations/*.sql        # ver nota debajo
│   └── config/
│       └── config.go                  # carga de configuración (env, archivo)
├── docs/
└── go.mod
```

> **Nota sobre `migrations/`**: la Fase 4 ubicó las migraciones en `internal/storage/sqlite/migrations/` en vez del `migrations/` a nivel de repositorio sugerido originalmente en este documento. `go:embed` no admite patrones que suban de directorio (`../`), por lo que el único lugar válido para embeberlas junto al código que las aplica es dentro del propio paquete `internal/storage/sqlite`. Mantiene igualmente el objetivo de binario único: las migraciones viajan embebidas en el ejecutable, no como archivos sueltos a distribuir aparte.

## 2.3 Contrato interno: interfaz `Integration`

Cada integración (AMD, SAP, PEL, etc.) implementa una interfaz común que el núcleo usa para enrutar e invocar, sin conocer los detalles internos de cada una:

```go
package core

type Direction string

const (
    DirectionOutbound      Direction = "OUTBOUND"       // SGP -> externo
    DirectionInbound       Direction = "INBOUND"        // externo -> SGP
    DirectionBidirectional Direction = "BIDIRECTIONAL"
)

type Mode string

const (
    ModeSync  Mode = "SYNC"
    ModeAsync Mode = "ASYNC"
)

type Metadata struct {
    ID          string    // ej. "sgp-to-amd-envio-minuta"
    Name        string
    Direction   Direction
    Mode        Mode
    Version     string
}

type SendRequest struct {
    CorrelationID string
    Payload       json.RawMessage
}

type SendResult struct {
    Status  string // "SUCCESS" | "ERROR" | "PARTIAL"
    Data    any
    Message string
}

// Integration es el contrato que todo módulo de integración debe implementar.
type Integration interface {
    Metadata() Metadata
    // HandleSend procesa una invocación síncrona o el disparo de una asíncrona.
    HandleSend(ctx context.Context, req SendRequest) (SendResult, error)
}

// AsyncIntegration es implementada opcionalmente por integraciones de modo ASYNC,
// y es invocada por el Job Manager para ejecutar el trabajo real en background.
type AsyncIntegration interface {
    Integration
    Execute(ctx context.Context, job *jobmanager.Job) error
}
```

## 2.4 Registro de integraciones

Nexus mantiene un **registro central** (`core.Registry`) que asocia cada `integration_id` con su implementación. El registro se construye explícitamente en `cmd/nexus/main.go` (evitando "magia" de `init()` implícitos, para que sea fácil ver qué integraciones están activas):

```go
func buildRegistry(cfg *config.Config) *core.Registry {
    reg := core.NewRegistry()

    reg.Register(amd.NewEnvioMinutaIntegration(cfg.AMD))
    reg.Register(amd.NewDescargaMinutaIntegration(cfg.AMD))
    reg.Register(sap.NewSincronizacionMaestrosIntegration(cfg.SAP))
    // ... nuevas integraciones se agregan aquí

    return reg
}
```

Esto permite que agregar una integración nueva sea, en el caso simple, agregar un paquete nuevo bajo `internal/integrations/` y una línea de registro — ver [Guía para Nueva Integración](09-guia-nueva-integracion.md).

## 2.5 Capa de adaptadores reutilizables

Para evitar que cada integración reimplemente lógica de bajo nivel, `internal/adapters` provee clientes genéricos y reutilizables:

- **`restclient`**: cliente HTTP con soporte de reintentos, timeouts configurables, inyección de headers de autenticación, y logging de requests/responses (con redacción de datos sensibles).
- **`soapclient`**: cliente SOAP/WSDL genérico (envelope XML, manejo de `Fault`, soporte WS-Security si corresponde).
- **`dbclient`**: wrapper sobre `database/sql` (o driver específico) para integraciones que requieren conexión directa a base de datos externa, con manejo de pool de conexiones y timeouts.

Cada integración específica (ej. `internal/integrations/amd`) usa estos adaptadores y les agrega la lógica propia del sistema externo: endpoints, formato de autenticación, mapeo de campos.

## 2.6 Decisiones técnicas sugeridas

| Aspecto | Decisión sugerida | Justificación |
|---|---|---|
| Lenguaje / runtime | Go (binario único) | Requisito del proyecto; facilita despliegue. |
| Router HTTP | `chi` o `net/http` + `ServeMux` (Go 1.22+) | Livianos, sin dependencias pesadas, alineados con "binario único". |
| Logging estructurado | `log/slog` (stdlib desde Go 1.21) | Evita dependencia externa, logging estructurado nativo. |
| Base de datos de Nexus | **SQLite** durante la prueba de concepto; **PostgreSQL** como destino de madurez (a confirmar según estándar Sodexo) | SQLite permite iterar rápido sin infraestructura adicional y refuerza el "binario único" (archivo embebido, sin servidor de BD que levantar). Persistencia de jobs, auditoría, catálogo de integraciones y credenciales. |
| Procesamiento asíncrono | Worker pool interno + tabla de jobs (sin broker externo en esta fase) | Mantiene el binario autocontenido; evita infraestructura adicional. |
| Configuración | Variables de entorno + archivo de configuración (ej. YAML) con validación al arranque | Permite distintos entornos (dev/test/prod) sin recompilar. |
| Observabilidad | Logs estructurados + métricas básicas (ej. `expvar` o Prometheus client) | Monitoreo operacional sin acoplar a una plataforma específica. |

Estas decisiones son sugerencias de partida; deben confirmarse con el equipo según los estándares de infraestructura de Sodexo antes de la implementación.

> **Nota sobre la base de datos (fase de prueba de concepto)**: mientras el proyecto está en etapa de PoC, Nexus usa **SQLite** como motor de persistencia (un único archivo `.db`, sin servidor externo), lo que simplifica el desarrollo y las pruebas locales. El acceso a datos debe implementarse a través de `database/sql` con sentencias SQL estándar (evitando funciones o tipos específicos de SQLite) y, de ser posible, con un query builder/ORM liviano que soporte múltiples dialectos (ej. `sqlx` + migraciones compatibles), de forma que la migración futura a **PostgreSQL** —cuando el proyecto madure— implique principalmente cambiar el driver y el DSN de conexión, no reescribir la capa de acceso a datos. Ver detalle de consideraciones de compatibilidad en [Modelo de Datos §8.8](08-modelo-datos.md#88-nota-sobre-el-motor-de-base-de-datos-sqlite-en-la-poc--postgresql-a-futuro).
>
> **Driver elegido**: `modernc.org/sqlite` — una reimplementación de SQLite en Go puro (sin CGO). Se descartó `mattn/go-sqlite3` (el binding más popular) porque requiere un compilador de C disponible en cada máquina que compile Nexus, lo que choca con el objetivo de "binario único, fácil de compilar en cualquier entorno" y además no es viable en el entorno de desarrollo usado para esta PoC (Windows sin toolchain de C instalado).
>
> **Atención al actualizar esta dependencia**: a partir de `modernc.org/sqlite v1.60.1` (y de su dependencia transitiva `golang.org/x/sys`), el paquete exige Go **≥ 1.26**. Para no forzar esa migración de toolchain sin decisión explícita del equipo, `go.mod` fija versiones más antiguas y compatibles con Go 1.24 (`modernc.org/sqlite v1.34.1`, `modernc.org/libc v1.55.3`, `golang.org/x/sys v0.27.0`/`v0.31.0`). Un `go get -u` o `go get modernc.org/sqlite@latest` sin cuidado volvería a subir el requisito a Go 1.26 — si se decide adoptar Go 1.26, hacerlo como una decisión consciente (actualizar también `go.mod` y la versión de Go documentada en `README.md`), no como efecto secundario de actualizar una dependencia.

## 2.7 Middlewares de la API

Toda solicitud entrante pasa por una cadena de middlewares antes de llegar al handler:

1. **Recovery**: captura panics y los convierte en respuesta HTTP 500 controlada, sin tumbar el proceso.
2. **Logging**: registra método, ruta, `correlation_id`, duración y código de resultado.
3. **Autenticación**: valida API Key / JWT (ver [Autenticación y Seguridad](06-autenticacion-seguridad.md)).
4. **Rate limiting** (opcional, por cliente/integración): protege a los sistemas externos de sobrecarga.
5. **Validación de envelope**: valida la estructura genérica antes de enrutar a la integración específica.

## 2.8 Flujo interno de una solicitud síncrona

1. Handler `POST /integrations/{integration_id}/send` recibe el envelope.
2. Middleware de autenticación valida el token/API Key del cliente.
3. El núcleo valida la estructura del envelope y busca la integración en el `Registry` por `integration_id`.
4. Si no existe, responde `404` con código de error `INTEGRATION_NOT_FOUND`.
5. Si existe y es de modo `SYNC`, se invoca `HandleSend` directamente, se espera el resultado y se responde en el mismo request.
6. Si es de modo `ASYNC`, se crea un `Job`, se encola para el worker pool y se responde inmediatamente con `job_id` (ver [Patrón de Integración Asíncrona](05-patron-asincrono.md)).
7. En ambos casos, se registra el evento en auditoría y en logs técnicos, incluyendo el resultado de la comunicación con el sistema externo.
