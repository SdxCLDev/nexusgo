package handlers

import (
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"nexusgo/internal/api/apierr"
	"nexusgo/internal/api/dto"
	"nexusgo/internal/api/httpx"
	"nexusgo/internal/core"
	"nexusgo/internal/jobs"
)

const (
	defaultJobListLimit = 50
	maxJobListLimit     = 200
)

// JobList implementa GET /api/v1/jobs — listado paginado de jobs, filtrable por
// integration_id, del más reciente al más antiguo. Permite descubrir descargas
// pasadas (ej. de amd-to-sgp-descarga-minuta) sin recordar el job_id. Usa
// paginación por cursor sobre offset — ver docs/03-contrato-api-rest.md §3.10.
func JobList(store jobs.Store, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		integrationID := q.Get("integration_id")

		limit := defaultJobListLimit
		if raw := q.Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n <= 0 {
				apierr.Write(w, logger, "", integrationID, core.NewInvalidRequestError("limit inválido: %q", raw))
				return
			}
			limit = n
			if limit > maxJobListLimit {
				limit = maxJobListLimit
			}
		}

		offset, err := decodeCursor(q.Get("cursor"))
		if err != nil {
			apierr.Write(w, logger, "", integrationID, core.NewInvalidRequestError("cursor inválido"))
			return
		}

		// Pedimos un elemento de más para saber si hay página siguiente sin un
		// COUNT aparte.
		list, err := store.List(r.Context(), integrationID, limit+1, offset)
		if err != nil {
			apierr.Write(w, logger, "", integrationID, core.NewInternalError("no se pudieron listar los jobs: %w", err))
			return
		}

		hasMore := len(list) > limit
		if hasMore {
			list = list[:limit]
		}

		items := make([]dto.JobListItem, 0, len(list))
		for _, job := range list {
			item := dto.JobListItem{
				JobID:         job.ID,
				IntegrationID: job.IntegrationID,
				CorrelationID: job.CorrelationID,
				Status:        string(job.Status),
				Progress: dto.JobProgress{
					Total:     job.ProgressTotal,
					Processed: job.ProgressProcessed,
					Failed:    job.ProgressFailed,
				},
				CreatedAt: job.CreatedAt.UTC().Format(time.RFC3339),
				UpdatedAt: job.UpdatedAt.UTC().Format(time.RFC3339),
			}
			if job.FinishedAt != nil {
				finished := job.FinishedAt.UTC().Format(time.RFC3339)
				item.FinishedAt = &finished
			}
			items = append(items, item)
		}

		resp := dto.JobListResponse{Items: items, HasMore: hasMore}
		if hasMore {
			resp.NextCursor = encodeCursor(offset + limit)
		}
		httpx.WriteJSON(w, http.StatusOK, resp)
	}
}

// cursor es el contenido opaco del token de paginación — ver §3.10. Se
// serializa como JSON base64 (ej. {"offset":50} => "eyJvZmZzZXQiOjUwfQ==").
type cursor struct {
	Offset int `json:"offset"`
}

func encodeCursor(offset int) string {
	b, _ := json.Marshal(cursor{Offset: offset})
	return base64.StdEncoding.EncodeToString(b)
}

func decodeCursor(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return 0, err
	}
	var c cursor
	if err := json.Unmarshal(b, &c); err != nil {
		return 0, err
	}
	if c.Offset < 0 {
		return 0, strconv.ErrRange
	}
	return c.Offset, nil
}
