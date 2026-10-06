package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"nexusgo/internal/api/apierr"
	"nexusgo/internal/api/dto"
	"nexusgo/internal/api/httpx"
	"nexusgo/internal/audit"
	"nexusgo/internal/auth"
	"nexusgo/internal/core"
	"nexusgo/internal/core/idempotency"
	"nexusgo/internal/core/jobmanager"
)

// Send implementa POST /api/v1/integrations/{integration_id}/send — ver
// docs/03-contrato-api-rest.md §3.4-3.6, docs/02-arquitectura.md §2.8 y la
// idempotencia por correlation_id de docs/03-contrato-api-rest.md §3.9.
// Se registra detrás del middleware auth.Authenticate (ver router.go), que
// deja los claims del token en el contexto de la solicitud.
//
// Nota: la idempotencia por correlation_id (ver internal/core/idempotency)
// solo aplica al camino síncrono. Las integraciones asíncronas no la usan
// todavía — ver el pendiente anotado en docs/10-plan-de-trabajo-poc.md Fase 5.
func Send(reg *core.Registry, jobManager *jobmanager.Manager, auditStore audit.Store, idem *idempotency.Store, logger *slog.Logger) http.HandlerFunc {
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
			asyncIntegration, ok := integration.(core.AsyncIntegration)
			if !ok {
				// Error de configuración: una integración registrada como
				// ASYNC debe implementar core.AsyncIntegration.
				apierr.Write(w, logger, env.CorrelationID, integrationID,
					core.NewInternalError("la integración %q está marcada ASYNC pero no implementa AsyncIntegration", integrationID))
				return
			}

			jobID, err := jobManager.Submit(r.Context(), asyncIntegration, env.CorrelationID, claims.Subject, env.Payload)
			if err != nil {
				apierr.Write(w, logger, env.CorrelationID, integrationID,
					core.NewInternalError("no se pudo encolar el job: %w", err))
				return
			}

			httpx.WriteJSON(w, http.StatusAccepted, dto.AsyncAcceptedResponse{
				CorrelationID: env.CorrelationID,
				IntegrationID: integrationID,
				JobID:         jobID,
				Status:        "ACCEPTED",
				StatusURL:     "/api/v1/jobs/" + jobID,
				Timestamp:     time.Now().UTC().Format(time.RFC3339),
			})
			return
		}

		cached, inProgress := idem.Begin(integrationID, env.CorrelationID)
		if inProgress {
			apierr.Write(w, logger, env.CorrelationID, integrationID,
				core.NewDuplicateError("ya existe una solicitud en curso con correlation_id %q para %q", env.CorrelationID, integrationID))
			return
		}
		if cached != nil {
			logger.Info("solicitud duplicada: se devuelve el resultado cacheado",
				"correlation_id", env.CorrelationID, "integration_id", integrationID)
			writeSendSuccess(w, env.CorrelationID, integrationID, *cached)
			return
		}

		auditID := core.NewID()
		startedAt := time.Now().UTC()
		if err := auditStore.Start(r.Context(), audit.Record{
			AuditID:        auditID,
			CorrelationID:  env.CorrelationID,
			IntegrationID:  integrationID,
			ClientID:       claims.Subject,
			ExternalSystem: meta.ExternalSystem,
			Direction:      string(meta.Direction),
			Mode:           string(meta.Mode),
			// Nota: el payload se guarda sin redactar durante la PoC (solo
			// datos de prueba circulan por mock-echo). La redacción de campos
			// sensibles para integraciones reales es trabajo de la Fase 9 —
			// ver docs/06-autenticacion-seguridad.md §6.6.
			RequestSummary: env.Payload,
			StartedAt:      startedAt,
		}); err != nil {
			logger.Error("no se pudo iniciar el registro de auditoría", "audit_id", auditID, "error", err)
		}

		result, err := callIntegration(r.Context(), integration, core.SendRequest{
			CorrelationID: env.CorrelationID,
			Payload:       env.Payload,
		})

		finishedAt := time.Now().UTC()
		if err != nil {
			idem.Fail(integrationID, env.CorrelationID)
			if ferr := auditStore.Finish(r.Context(), auditID, audit.StatusFailed, nil, err.Error(), finishedAt); ferr != nil {
				logger.Error("no se pudo finalizar el registro de auditoría", "audit_id", auditID, "error", ferr)
			}
			apierr.Write(w, logger, env.CorrelationID, integrationID, err)
			return
		}

		idem.Complete(integrationID, env.CorrelationID, result)

		status := audit.StatusSuccess
		if result.Status == "PARTIAL" {
			status = audit.StatusPartial
		}
		if ferr := auditStore.Finish(r.Context(), auditID, status, result.Data, "", finishedAt); ferr != nil {
			logger.Error("no se pudo finalizar el registro de auditoría", "audit_id", auditID, "error", ferr)
		}

		writeSendSuccess(w, env.CorrelationID, integrationID, result)
	}
}

// callIntegration invoca HandleSend recuperando cualquier panic y
// convirtiéndolo en un *core.Error. Sin esto, un panic de la integración
// saltaría directo al middleware Recover (ver docs/02-arquitectura.md §2.7),
// que no conoce correlation_id/integration_id ni libera la clave de
// idempotencia ni cierra el registro de auditoría — dejándolo INICIADO para
// siempre y bloqueando reintentos con el mismo correlation_id.
func callIntegration(ctx context.Context, integration core.Integration, req core.SendRequest) (result core.SendResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = core.NewInternalError("panic en la integración: %v", r)
		}
	}()
	return integration.HandleSend(ctx, req)
}

func writeSendSuccess(w http.ResponseWriter, correlationID, integrationID string, result core.SendResult) {
	httpx.WriteJSON(w, http.StatusOK, dto.SendSuccessResponse{
		CorrelationID: correlationID,
		IntegrationID: integrationID,
		Status:        result.Status,
		Code:          "NEXUS_OK",
		Message:       result.Message,
		Data:          result.Data,
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
	})
}
