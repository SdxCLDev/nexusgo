// Package api define el enrutamiento HTTP de Nexus.
package api

import (
	"log/slog"
	"net/http"
	"time"

	"nexusgo/internal/api/handlers"
	"nexusgo/internal/api/middleware"
	"nexusgo/internal/audit"
	"nexusgo/internal/auth"
	"nexusgo/internal/core"
	"nexusgo/internal/core/idempotency"
	"nexusgo/internal/core/jobmanager"
	"nexusgo/internal/jobs"
)

// Deps son las dependencias del router. Se agrupan en un struct porque la
// lista de parámetros individuales ya era larga y crece con cada fase del
// plan de trabajo (ver docs/10-plan-de-trabajo-poc.md).
type Deps struct {
	Logger       *slog.Logger
	Registry     *core.Registry
	CatalogStore core.CatalogStore
	ClientStore  auth.ClientStore
	JWTSecret    []byte
	TokenTTL     time.Duration
	AuditStore   audit.Store
	Idempotency  *idempotency.Store
	JobStore     jobs.Store
	JobManager   *jobmanager.Manager
	ReadyChecks  []handlers.ReadyCheck

	// BasePath es el prefijo bajo el que un reverse proxy expone a Nexus
	// (ej. "/nexus"). Vacío si no hay proxy o si está publicado en la raíz
	// — ver internal/config.Config.PublicBasePath.
	BasePath string
}

func NewRouter(deps Deps) http.Handler {
	mux := http.NewServeMux()

	// Endpoints de infraestructura: fuera del versionado /api/v1 a propósito,
	// para que un balanceador/orquestador los consulte sin conocer la
	// versión del contrato funcional.
	mux.HandleFunc("GET /health", handlers.Health)
	mux.HandleFunc("GET /ready", handlers.Ready(deps.ReadyChecks...))

	// Documentación interactiva (Swagger UI) — sin autenticación: describe el
	// contrato, no expone datos. Útil para explorar/probar la API desde un
	// navegador sin acceso al código fuente (ver docs/10-plan-de-trabajo-poc.md).
	mux.HandleFunc("GET /openapi.json", handlers.OpenAPISpec(deps.BasePath))
	mux.HandleFunc("GET /docs", handlers.SwaggerUI(deps.BasePath))

	// Emisión de token: sin autenticación previa (es el punto de entrada).
	mux.HandleFunc("POST /api/v1/auth/token", handlers.IssueToken(deps.ClientStore, deps.JWTSecret, deps.TokenTTL, deps.Logger))

	// Endpoints funcionales: protegidos con autenticación JWT. Los
	// endpoints de jobs no exigen un scope adicional (ver nota de
	// simplificación en docs/10-plan-de-trabajo-poc.md Fase 5): basta con
	// estar autenticado, igual que el catálogo.
	authn := middleware.Authenticate(deps.JWTSecret, deps.Logger)
	mux.Handle("POST /api/v1/integrations/{integration_id}/send",
		authn(handlers.Send(deps.Registry, deps.JobManager, deps.AuditStore, deps.Idempotency, deps.BasePath, deps.Logger)))
	mux.Handle("GET /api/v1/integrations", authn(handlers.Catalog(deps.CatalogStore, deps.Logger)))
	mux.Handle("GET /api/v1/jobs", authn(handlers.JobList(deps.JobStore, deps.Logger)))
	mux.Handle("GET /api/v1/jobs/{job_id}", authn(handlers.JobStatus(deps.JobStore, deps.BasePath, deps.Logger)))
	mux.Handle("GET /api/v1/jobs/{job_id}/result", authn(handlers.JobResult(deps.JobStore, deps.Logger)))
	mux.Handle("POST /api/v1/jobs/{job_id}/ack", authn(handlers.JobAck(deps.JobStore, deps.Logger)))

	var h http.Handler = mux
	h = middleware.Logging(deps.Logger)(h)
	h = middleware.Recover(deps.Logger)(h)

	return h
}
