package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"nexusgo/internal/audit"
)

// AuditStore implementa audit.Store sobre SQLite — ver
// docs/08-modelo-datos.md §8.4. Reemplaza a audit.InMemoryStore en
// producción (Fase 4); InMemoryStore se mantiene para pruebas rápidas del
// paquete internal/api.
type AuditStore struct {
	db *sql.DB
}

func NewAuditStore(db *sql.DB) *AuditStore {
	return &AuditStore{db: db}
}

func (s *AuditStore) Start(ctx context.Context, rec audit.Record) error {
	reqJSON, err := marshalNullable(rec.RequestSummary)
	if err != nil {
		return fmt.Errorf("sqlite: no se pudo serializar request_summary: %w", err)
	}

	var jobID any
	if rec.JobID != "" {
		jobID = rec.JobID
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO audit_log (
			audit_id, correlation_id, job_id, integration_id, client_id,
			external_system, direction, mode, status, request_summary, started_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, rec.AuditID, rec.CorrelationID, jobID, rec.IntegrationID, rec.ClientID,
		rec.ExternalSystem, rec.Direction, rec.Mode, string(audit.StatusStarted),
		reqJSON, rec.StartedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("sqlite: no se pudo iniciar la auditoría %q: %w", rec.AuditID, err)
	}
	return nil
}

func (s *AuditStore) Finish(ctx context.Context, auditID string, status audit.Status, responseSummary any, errDetail string, finishedAt time.Time) error {
	var startedAtStr string
	if err := s.db.QueryRowContext(ctx,
		`SELECT started_at FROM audit_log WHERE audit_id = ?`, auditID,
	).Scan(&startedAtStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("sqlite: el registro de auditoría %q no existe", auditID)
		}
		return fmt.Errorf("sqlite: error leyendo la auditoría %q: %w", auditID, err)
	}

	startedAt, err := time.Parse(time.RFC3339, startedAtStr)
	if err != nil {
		return fmt.Errorf("sqlite: started_at corrupto para %q: %w", auditID, err)
	}

	respJSON, err := marshalNullable(responseSummary)
	if err != nil {
		return fmt.Errorf("sqlite: no se pudo serializar response_summary: %w", err)
	}

	var errDetailArg any
	if errDetail != "" {
		errDetailArg = errDetail
	}

	durationMs := finishedAt.Sub(startedAt).Milliseconds()

	// La cláusula "AND finished_at IS NULL" aplica a nivel de base de datos
	// la inmutabilidad de docs/07-logging-auditoria.md §7.2.4: un registro ya
	// terminado no se vuelve a actualizar.
	res, err := s.db.ExecContext(ctx, `
		UPDATE audit_log
		SET status = ?, response_summary = ?, error_detail = ?, finished_at = ?, duration_ms = ?
		WHERE audit_id = ? AND finished_at IS NULL
	`, string(status), respJSON, errDetailArg, finishedAt.UTC().Format(time.RFC3339), durationMs, auditID)
	if err != nil {
		return fmt.Errorf("sqlite: no se pudo finalizar la auditoría %q: %w", auditID, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: no se pudo verificar la actualización de %q: %w", auditID, err)
	}
	if n == 0 {
		return fmt.Errorf("sqlite: el registro %q ya tiene estado final y es inmutable", auditID)
	}
	return nil
}

func marshalNullable(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	if raw, ok := v.(json.RawMessage); ok {
		if len(raw) == 0 {
			return nil, nil
		}
		return string(raw), nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}
