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

// Send implementa POST /api/v1/integrations/{integration_id}/send — ver
// docs/03-contrato-api-rest.md §3.4-3.5 y docs/02-arquitectura.md §2.8.
// Se registra detrás del middleware auth.Authenticate (ver router.go), que
// deja los claims del token en el contexto de la solicitud.
func Send(reg *core.Registry, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		integrationID := r.PathValue("integration_id")

		var env core.Envelope
		if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
			apierr.Write(w, logger, "", integrationID, core.NewEnvelopeError("cuerpo JSON inválido: %v", err))
			return
		}

		if verr := env.Validate(); verr != nil {
			apierr.Write(w, logger, env.CorrelationID, integrationID, verr)
			return
		}

		if env.CorrelationID == "" {
			env.CorrelationID = core.NewID()
		}

		integration, ok := reg.Get(integrationID)
		if !ok {
			apierr.Write(w, logger, env.CorrelationID, integrationID,
				core.NewNotFoundError("la integración %q no existe", integrationID))
			return
		}

		claims, ok := auth.ClaimsFromContext(r.Context())
		if !ok {
			// No debería ocurrir si el middleware de autenticación corrió antes.
			apierr.Write(w, logger, env.CorrelationID, integrationID,
				core.NewInternalError("contexto de autenticación ausente"))
			return
		}
		requiredScope := "integration:" + integrationID + ":invoke"
		if !claims.HasScope(requiredScope) {
			apierr.Write(w, logger, env.CorrelationID, integrationID,
				core.NewForbiddenError("el cliente %q no tiene permiso para invocar %q", claims.Subject, integrationID))
			return
		}

		meta := integration.Metadata()
		if meta.Mode == core.ModeAsync {
			// El Job Manager se incorpora en la Fase 5 — por ahora las
			// integraciones asíncronas no pueden invocarse.
			apierr.Write(w, logger, env.CorrelationID, integrationID,
				core.NewUnavailableError("la integración %q es asíncrona; el soporte asíncrono aún no está implementado", integrationID))
			return
		}

		result, err := integration.HandleSend(r.Context(), core.SendRequest{
			CorrelationID: env.CorrelationID,
			Payload:       env.Payload,
		})
		if err != nil {
			apierr.Write(w, logger, env.CorrelationID, integrationID, err)
			return
		}

		httpx.WriteJSON(w, http.StatusOK, dto.SendSuccessResponse{
			CorrelationID: env.CorrelationID,
			IntegrationID: integrationID,
			Status:        result.Status,
			Code:          "NEXUS_OK",
			Message:       result.Message,
			Data:          result.Data,
			Timestamp:     time.Now().UTC().Format(time.RFC3339),
		})
	}
}
