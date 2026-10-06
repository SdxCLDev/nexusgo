package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"nexusgo/internal/jobs"
)

// JobStore implementa jobs.Store sobre SQLite — ver docs/08-modelo-datos.md
// §8.3/§8.3.1.
type JobStore struct {
	db *sql.DB
}

func NewJobStore(db *sql.DB) *JobStore {
	return &JobStore{db: db}
}

func (s *JobStore) Create(ctx context.Context, job jobs.Job) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO jobs (job_id, integration_id, correlation_id, client_id, status, delivery_mode, progress_processed, progress_failed, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 0, 0, ?, ?)
	`, job.ID, job.IntegrationID, job.CorrelationID, job.ClientID, string(job.Status), string(job.DeliveryMode),
		job.CreatedAt.UTC().Format(time.RFC3339), job.UpdatedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("sqlite: no se pudo crear el job %q: %w", job.ID, err)
	}
	return nil
}

func (s *JobStore) MarkRunning(ctx context.Context, jobID string, at time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE jobs SET status = ?, updated_at = ? WHERE job_id = ? AND status = 'PENDING'
	`, string(jobs.StatusRunning), at.UTC().Format(time.RFC3339), jobID)
	if err != nil {
		return fmt.Errorf("sqlite: no se pudo marcar RUNNING el job %q: %w", jobID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("sqlite: el job %q no existe o no está en PENDING", jobID)
	}
	return nil
}

func (s *JobStore) UpdateProgress(ctx context.Context, jobID string, processed, failed int, total *int) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE jobs SET progress_processed = ?, progress_failed = ?, progress_total = COALESCE(?, progress_total), updated_at = ?
		WHERE job_id = ?
	`, processed, failed, total, time.Now().UTC().Format(time.RFC3339), jobID)
	if err != nil {
		return fmt.Errorf("sqlite: no se pudo actualizar el progreso del job %q: %w", jobID, err)
	}
	return nil
}

// Finish aplica a nivel SQL la misma inmutabilidad que audit.Store.Finish
// (ver docs/07-logging-auditoria.md §7.2.4): un job ya terminado no se
// vuelve a actualizar.
func (s *JobStore) Finish(ctx context.Context, jobID string, status jobs.Status, resultSummary any, finishedAt time.Time) error {
	summaryJSON, err := marshalNullable(resultSummary)
	if err != nil {
		return fmt.Errorf("sqlite: no se pudo serializar result_summary: %w", err)
	}

	res, err := s.db.ExecContext(ctx, `
		UPDATE jobs SET status = ?, result_summary = ?, finished_at = ?, updated_at = ?
		WHERE job_id = ? AND finished_at IS NULL
	`, string(status), summaryJSON, finishedAt.UTC().Format(time.RFC3339), finishedAt.UTC().Format(time.RFC3339), jobID)
	if err != nil {
		return fmt.Errorf("sqlite: no se pudo finalizar el job %q: %w", jobID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: no se pudo verificar la actualización del job %q: %w", jobID, err)
	}
	if n == 0 {
		return fmt.Errorf("sqlite: el job %q ya tiene estado final y es inmutable, o no existe", jobID)
	}
	return nil
}

func (s *JobStore) Ack(ctx context.Context, jobID string, ackedAt time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE jobs SET acked_at = ? WHERE job_id = ?`, ackedAt.UTC().Format(time.RFC3339), jobID)
	if err != nil {
		return fmt.Errorf("sqlite: no se pudo confirmar (ack) el job %q: %w", jobID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("sqlite: el job %q no existe", jobID)
	}
	return nil
}

func (s *JobStore) Get(ctx context.Context, jobID string) (jobs.Job, bool, error) {
	var j jobs.Job
	var status, deliveryMode string
	var progressTotal sql.NullInt64
	var resultSummaryText sql.NullString
	var createdAtStr, updatedAtStr string
	var finishedAtStr, ackedAtStr sql.NullString

	err := s.db.QueryRowContext(ctx, `
		SELECT job_id, integration_id, correlation_id, client_id, status, delivery_mode,
		       progress_total, progress_processed, progress_failed, result_summary,
		       created_at, updated_at, finished_at, acked_at
		FROM jobs WHERE job_id = ?
	`, jobID).Scan(
		&j.ID, &j.IntegrationID, &j.CorrelationID, &j.ClientID, &status, &deliveryMode,
		&progressTotal, &j.ProgressProcessed, &j.ProgressFailed, &resultSummaryText,
		&createdAtStr, &updatedAtStr, &finishedAtStr, &ackedAtStr,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return jobs.Job{}, false, nil
	}
	if err != nil {
		return jobs.Job{}, false, fmt.Errorf("sqlite: error buscando el job %q: %w", jobID, err)
	}

	j.Status = jobs.Status(status)
	j.DeliveryMode = jobs.DeliveryMode(deliveryMode)
	if progressTotal.Valid {
		t := int(progressTotal.Int64)
		j.ProgressTotal = &t
	}
	if resultSummaryText.Valid {
		var summary any
		if err := json.Unmarshal([]byte(resultSummaryText.String), &summary); err == nil {
			j.ResultSummary = summary
		}
	}
	j.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
	j.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAtStr)
	if finishedAtStr.Valid {
		t, _ := time.Parse(time.RFC3339, finishedAtStr.String)
		j.FinishedAt = &t
	}
	if ackedAtStr.Valid {
		t, _ := time.Parse(time.RFC3339, ackedAtStr.String)
		j.AckedAt = &t
	}

	return j, true, nil
}

func (s *JobStore) AddItem(ctx context.Context, item jobs.Item) error {
	dataJSON, err := marshalNullable(item.Data)
	if err != nil {
		return fmt.Errorf("sqlite: no se pudo serializar data del ítem: %w", err)
	}
	var errDetailArg any
	if item.ErrorDetail != "" {
		errDetailArg = item.ErrorDetail
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO job_items (job_item_id, job_id, external_id, status, data, error_detail, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, item.ID, item.JobID, item.ExternalID, item.Status, dataJSON, errDetailArg, item.CreatedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("sqlite: no se pudo guardar el ítem %q del job %q: %w", item.ExternalID, item.JobID, err)
	}
	return nil
}

func (s *JobStore) ListItems(ctx context.Context, jobID string) ([]jobs.Item, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT job_item_id, job_id, external_id, status, data, error_detail, created_at
		FROM job_items WHERE job_id = ? ORDER BY created_at, job_item_id
	`, jobID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: no se pudieron listar los ítems del job %q: %w", jobID, err)
	}
	defer rows.Close()

	var items []jobs.Item
	for rows.Next() {
		var it jobs.Item
		var dataText, errDetail sql.NullString
		var createdAtStr string
		if err := rows.Scan(&it.ID, &it.JobID, &it.ExternalID, &it.Status, &dataText, &errDetail, &createdAtStr); err != nil {
			return nil, fmt.Errorf("sqlite: error leyendo ítems del job %q: %w", jobID, err)
		}
		if dataText.Valid {
			var data any
			if err := json.Unmarshal([]byte(dataText.String), &data); err == nil {
				it.Data = data
			}
		}
		it.ErrorDetail = errDetail.String
		it.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
		items = append(items, it)
	}
	return items, rows.Err()
}
