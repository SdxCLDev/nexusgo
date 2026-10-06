package handlers

import (
	"net/http"

	"nexusgo/internal/api/httpx"
)

// ReadyCheck es una verificación de dependencia (ej. base de datos) para /ready.
type ReadyCheck func() error

func Health(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func Ready(checks ...ReadyCheck) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		for _, check := range checks {
			if err := check(); err != nil {
				httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{
					"status": "not_ready",
					"error":  err.Error(),
				})
				return
			}
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}
