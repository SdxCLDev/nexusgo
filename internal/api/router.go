// Package api define el enrutamiento HTTP de Nexus.
package api

import (
	"log/slog"
	"net/http"

	"nexusgo/internal/api/handlers"
	"nexusgo/internal/api/middleware"
)

func NewRouter(logger *slog.Logger, readyChecks ...handlers.ReadyCheck) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", handlers.Health)
	mux.HandleFunc("GET /ready", handlers.Ready(readyChecks...))

	var h http.Handler = mux
	h = middleware.Logging(logger)(h)
	h = middleware.Recover(logger)(h)

	return h
}
