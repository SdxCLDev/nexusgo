package middleware

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"nexusgo/internal/api/apierr"
	"nexusgo/internal/auth"
	"nexusgo/internal/core"
)

const bearerPrefix = "Bearer "

// Authenticate valida el JWT del header Authorization y deja sus claims en
// el contexto de la solicitud (ver auth.ClaimsFromContext). No valida scopes:
// eso es responsabilidad de cada handler, que conoce el integration_id de la
// ruta — ver docs/06-autenticacion-seguridad.md §6.1.4.
func Authenticate(jwtSecret []byte, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if !strings.HasPrefix(header, bearerPrefix) {
				apierr.Write(w, logger, "", "", core.NewUnauthorizedError("falta el header Authorization: Bearer <token>"))
				return
			}

			token := strings.TrimPrefix(header, bearerPrefix)
			claims, err := auth.ParseAndVerifyJWT(jwtSecret, token)
			if err != nil {
				if errors.Is(err, auth.ErrExpiredToken) {
					apierr.Write(w, logger, "", "", core.NewUnauthorizedError("token expirado"))
				} else {
					apierr.Write(w, logger, "", "", core.NewUnauthorizedError("token inválido"))
				}
				return
			}

			next.ServeHTTP(w, r.WithContext(auth.ContextWithClaims(r.Context(), claims)))
		})
	}
}
