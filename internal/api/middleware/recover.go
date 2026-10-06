package middleware

import (
	"log/slog"
	"net/http"

	"nexusgo/internal/api/httpx"
)

// Recover captura panics en los handlers y responde 500 en vez de tumbar el proceso.
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("panic recuperado",
						"error", rec,
						"path", r.URL.Path,
						"method", r.Method,
					)
					httpx.WriteJSON(w, http.StatusInternalServerError, map[string]string{
						"status":  "ERROR",
						"code":    "INTERNAL_ERROR",
						"message": "error interno del servidor",
					})
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
