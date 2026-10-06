// Package api define el enrutamiento HTTP de Nexus.
package api

import (
	"log/slog"
	"net/http"
	"time"

	"nexusgo/internal/api/handlers"
	"nexusgo/internal/api/middleware"
	"nexusgo/internal/auth"
	"nexusgo/internal/core"
)

func NewRouter(
	logger *slog.Logger,
	reg *core.Registry,
	clientStore auth.ClientStore,
	jwtSecret []byte,
	tokenTTL time.Duration,
	readyChecks ...handlers.ReadyCheck,
) http.Handler {
	mux := http.NewServeMux()

	// Endpoints de infraestructura: fuera del versionado /api/v1 a propósito,
	// para que un balanceador/orquestador los consulte sin conocer la
	// versión del contrato funcional.
	mux.HandleFunc("GET /health", handlers.Health)
	mux.HandleFunc("GET /ready", handlers.Ready(readyChecks...))

	// Emisión de token: sin autenticación previa (es el punto de entrada).
	mux.HandleFunc("POST /api/v1/auth/token", handlers.IssueToken(clientStore, jwtSecret, tokenTTL, logger))

	// Endpoints funcionales: protegidos con autenticación JWT.
	authn := middleware.Authenticate(jwtSecret, logger)
	mux.Handle("POST /api/v1/integrations/{integration_id}/send", authn(handlers.Send(reg, logger)))
	mux.Handle("GET /api/v1/integrations", authn(handlers.Catalog(reg)))

	var h http.Handler = mux
	h = middleware.Logging(logger)(h)
	h = middleware.Recover(logger)(h)

	return h
}
