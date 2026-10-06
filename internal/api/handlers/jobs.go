package handlers

import (
	"log/slog"
	"net/http"
	"time"

	"nexusgo/internal/api/apierr"
	"nexusgo/internal/api/dto"
	"nexusgo/internal/api/httpx"
	"nexusgo/internal/core"
	"nexusgo/internal/jobs"
)

func isTerminal(status jobs.Status) bool {
	switch status {
	case jobs.StatusCompleted, jobs.StatusFailed, jobs.StatusPartial:
		return true
	default:
		return false
	}
}

func jobStatusResponse(job jobs.Job, basePath string) dto.JobStatusResponse {
	resp := dto.JobStatusResponse{
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
		resp.FinishedAt = &finished
	}
	if isTerminal(job.Status) && job.DeliveryMode == jobs.DeliveryPullAPI {
		resp.ResultURL = basePath + "/api/v1/jobs/" + job.ID + "/result"
	}
	return resp
}

// JobStatus implementa GET /api/v1/jobs/{job_id} — ver
// docs/03-contrato-api-rest.md §3.3 y docs/05-patron-asincrono.md §5.6.
func JobStatus(store jobs.Store, basePath string, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobID := r.PathValue("job_id")

		job, found, err := store.Get(r.Context(), jobID)
		if err != nil {
			apierr.Write(w, logger, "", "", core.NewInternalError("no se pudo consultar el job: %w", err))
			return
		}
		if !found {
			apierr.Write(w, logger, "", "", core.NewJobNotFoundError("el job %q no existe", jobID))
			return
		}

		httpx.WriteJSON(w, http.StatusOK, jobStatusResponse(job, basePath))
	}
}

// JobResult implementa GET /api/v1/jobs/{job_id}/result — ver
// docs/05-patron-asincrono.md §5.7.
func JobResult(store jobs.Store, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobID := r.PathValue("job_id")

		job, found, err := store.Get(r.Context(), jobID)
		if err != nil {
			apierr.Write(w, logger, "", "", core.NewInternalError("no se pudo consultar el job: %w", err))
			return
		}
		if !found {
			apierr.Write(w, logger, "", "", core.NewJobNotFoundError("el job %q no existe", jobID))
			return
		}
		if !isTerminal(job.Status) {
			apierr.Write(w, logger, job.CorrelationID, job.IntegrationID,
				core.NewJobNotFinishedError("el job %q todavía está en estado %s", jobID, job.Status))
			return
		}

		items, err := store.ListItems(r.Context(), jobID)
		if err != nil {
			apierr.Write(w, logger, job.CorrelationID, job.IntegrationID,
				core.NewInternalError("no se pudieron obtener los ítems del job: %w", err))
			return
		}

		summary := dto.JobResultSummary{}
		responseItems := make([]dto.JobItemResponse, 0, len(items))
		for _, it := range items {
			summary.Total++
			if it.Status == "SUCCESS" {
				summary.Success++
			} else {
				summary.Failed++
			}
			responseItems = append(responseItems, dto.JobItemResponse{
				ExternalID: it.ExternalID,
				Status:     it.Status,
				Data:       it.Data,
				Error:      it.ErrorDetail,
			})
		}

		httpx.WriteJSON(w, http.StatusOK, dto.JobResultResponse{
			JobID:         job.ID,
			IntegrationID: job.IntegrationID,
			Summary:       summary,
			Items:         responseItems,
		})
	}
}

// JobAck implementa POST /api/v1/jobs/{job_id}/ack — ver
// docs/03-contrato-api-rest.md §3.3.
func JobAck(store jobs.Store, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobID := r.PathValue("job_id")

		_, found, err := store.Get(r.Context(), jobID)
		if err != nil {
			apierr.Write(w, logger, "", "", core.NewInternalError("no se pudo consultar el job: %w", err))
			return
		}
		if !found {
			apierr.Write(w, logger, "", "", core.NewJobNotFoundError("el job %q no existe", jobID))
			return
		}

		if err := store.Ack(r.Context(), jobID, time.Now().UTC()); err != nil {
			apierr.Write(w, logger, "", "", core.NewInternalError("no se pudo confirmar el job: %w", err))
			return
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]string{"job_id": jobID, "status": "ACKED"})
	}
}
