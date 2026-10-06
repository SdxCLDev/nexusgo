// Package apierr centraliza la traducción de errores de dominio al formato
// estándar de error del contrato — ver docs/03-contrato-api-rest.md §3.8.
// Lo usan tanto los handlers como los middlewares (ej. autenticación), que
// de otro modo duplicarían esta lógica.
package apierr

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"nexusgo/internal/api/dto"
	"nexusgo/internal/api/httpx"
	"nexusgo/internal/core"
)

// Write traduce err a la respuesta estándar. Un error que no sea *core.Error
// se trata como INTERNAL_ERROR y no expone su detalle al llamador (sí queda
// en el log técnico).
func Write(w http.ResponseWriter, logger *slog.Logger, correlationID, integrationID string, err error) {
	var coreErr *core.Error
	if !errors.As(err, &coreErr) {
		logger.Error("error interno no controlado",
			"correlation_id", correlationID,
			"integration_id", integrationID,
			"error", err,
		)
		coreErr = core.NewInternalError("error interno del servidor")
	} else if coreErr.HTTPStatus >= http.StatusInternalServerError {
		logger.Error("error de integración",
			"correlation_id", correlationID,
			"integration_id", integrationID,
			"code", coreErr.Code,
			"error", err,
		)
	}

	httpx.WriteJSON(w, coreErr.HTTPStatus, dto.ErrorResponse{
		CorrelationID: correlationID,
		IntegrationID: integrationID,
		Status:        "ERROR",
		Code:          coreErr.Code,
		Message:       coreErr.Message,
		Details:       coreErr.Details,
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
	})
}
