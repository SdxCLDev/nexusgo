// Package api define el enrutamiento HTTP de Nexus.
package api

import (
	"log/slog"
	"net/http"

	"nexusgo/internal/api/handlers"
	"nexusgo/internal/api/middleware"
	"nexusgo/internal/core"
)

func NewRouter(logger *slog.Logger, reg *core.Registry, readyChecks ...handlers.ReadyCheck) http.Handler {
	mux := http.NewServeMux()

	// Endpoints de infraestructura: fuera del versionado /api/v1 a propósito,
	// para que un balanceador/orquestador los consulte sin conocer la
	// versión del contrato funcional.
	mux.HandleFunc("GET /health", handlers.Health)
	mux.HandleFunc("GET /ready", handlers.Ready(readyChecks...))

	mux.HandleFunc("POST /api/v1/integrations/{integration_id}/send", handlers.Send(reg, logger))
	mux.HandleFunc("GET /api/v1/integrations", handlers.Catalog(reg))

	var h http.Handler = mux
	h = middleware.Logging(logger)(h)
	h = middleware.Recover(logger)(h)

	return h
}
