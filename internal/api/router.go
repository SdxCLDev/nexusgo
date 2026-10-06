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
	ReadyChecks  []handlers.ReadyCheck
}

func NewRouter(deps Deps) http.Handler {
	mux := http.NewServeMux()

	// Endpoints de infraestructura: fuera del versionado /api/v1 a propósito,
	// para que un balanceador/orquestador los consulte sin conocer la
	// versión del contrato funcional.
	mux.HandleFunc("GET /health", handlers.Health)
	mux.HandleFunc("GET /ready", handlers.Ready(deps.ReadyChecks...))

	// Emisión de token: sin autenticación previa (es el punto de entrada).
	mux.HandleFunc("POST /api/v1/auth/token", handlers.IssueToken(deps.ClientStore, deps.JWTSecret, deps.TokenTTL, deps.Logger))

	// Endpoints funcionales: protegidos con autenticación JWT.
	authn := middleware.Authenticate(deps.JWTSecret, deps.Logger)
	mux.Handle("POST /api/v1/integrations/{integration_id}/send",
		authn(handlers.Send(deps.Registry, deps.AuditStore, deps.Idempotency, deps.Logger)))
	mux.Handle("GET /api/v1/integrations", authn(handlers.Catalog(deps.CatalogStore, deps.Logger)))

	var h http.Handler = mux
	h = middleware.Logging(deps.Logger)(h)
	h = middleware.Recover(deps.Logger)(h)

	return h
}
