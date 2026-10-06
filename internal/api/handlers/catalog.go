package handlers

import (
	"net/http"

	"nexusgo/internal/api/dto"
	"nexusgo/internal/api/httpx"
	"nexusgo/internal/core"
)

// Catalog implementa GET /api/v1/integrations — ver docs/03-contrato-api-rest.md §3.7.
//
// En esta fase el catálogo refleja únicamente lo registrado en memoria al
// arrancar el proceso; status siempre se reporta "ACTIVE". El estado
// persistido (ej. integraciones deshabilitadas) llega con la Fase 4.
func Catalog(reg *core.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		metas := reg.List()

		items := make([]dto.IntegrationSummary, 0, len(metas))
		for _, m := range metas {
			items = append(items, dto.IntegrationSummary{
				IntegrationID: m.ID,
				Name:          m.Name,
				Direction:     string(m.Direction),
				Mode:          string(m.Mode),
				Version:       m.Version,
				Status:        "ACTIVE",
			})
		}

		httpx.WriteJSON(w, http.StatusOK, dto.CatalogResponse{Integrations: items})
	}
}
