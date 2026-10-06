package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"nexusgo/internal/api/apierr"
	"nexusgo/internal/api/dto"
	"nexusgo/internal/api/httpx"
	"nexusgo/internal/auth"
	"nexusgo/internal/core"
)

// IssueToken implementa POST /api/v1/auth/token — ver
// docs/06-autenticacion-seguridad.md §6.1.1.
func IssueToken(store auth.ClientStore, jwtSecret []byte, ttl time.Duration, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req dto.TokenRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			apierr.Write(w, logger, "", "", core.NewInvalidRequestError("cuerpo JSON inválido: %v", err))
			return
		}
		if req.ClientID == "" || req.APIKey == "" {
			apierr.Write(w, logger, "", "", core.NewInvalidRequestError("'client_id' y 'api_key' son obligatorios"))
			return
		}

		client, found, err := store.FindByID(r.Context(), req.ClientID)
		if err != nil {
			apierr.Write(w, logger, "", "", core.NewInternalError("error al buscar el cliente: %w", err))
			return
		}
		if !found || client.Status != auth.ClientStatusActive || !auth.VerifyAPIKey(req.APIKey, client.APIKeyHash) {
			apierr.Write(w, logger, "", "", core.NewUnauthorizedError("client_id o api_key inválidos"))
			return
		}

		now := time.Now().UTC()
		claims := auth.Claims{
			Subject:   client.ID,
			Scopes:    client.Scopes,
			IssuedAt:  now.Unix(),
			ExpiresAt: now.Add(ttl).Unix(),
			Issuer:    "nexus",
		}

		token, err := auth.IssueJWT(jwtSecret, claims)
		if err != nil {
			apierr.Write(w, logger, "", "", core.NewInternalError("no se pudo emitir el token: %w", err))
			return
		}

		httpx.WriteJSON(w, http.StatusOK, dto.TokenResponse{
			AccessToken: token,
			TokenType:   "Bearer",
			ExpiresIn:   int(ttl.Seconds()),
		})
	}
}
