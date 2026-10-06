package handlers

import (
	"log/slog"
	"net/http"

	"nexusgo/internal/api/apierr"
	"nexusgo/internal/api/dto"
	"nexusgo/internal/api/httpx"
	"nexusgo/internal/core"
)

// Catalog implementa GET /api/v1/integrations — ver docs/03-contrato-api-rest.md §3.7.
// Lee de core.CatalogStore (respaldado en SQLite en producción desde la
// Fase 4) para reflejar el status persistido, no solo lo compilado en el binario.
func Catalog(store core.CatalogStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries, err := store.Catalog(r.Context())
		if err != nil {
			apierr.Write(w, logger, "", "", core.NewInternalError("no se pudo obtener el catálogo: %w", err))
			return
		}

		items := make([]dto.IntegrationSummary, 0, len(entries))
		for _, e := range entries {
			items = append(items, dto.IntegrationSummary{
				IntegrationID: e.IntegrationID,
				Name:          e.Name,
				Direction:     e.Direction,
				Mode:          e.Mode,
				Version:       e.Version,
				Status:        e.Status,
			})
		}

		httpx.WriteJSON(w, http.StatusOK, dto.CatalogResponse{Integrations: items})
	}
}
