package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// SimulatedInboxStore implementa mock.SimulatedInbox: representa, solo para
// esta PoC, la base de datos de SGP recibiendo registros en el modo de
// entrega push_db — ver docs/05-patron-asincrono.md §5.5(a).
type SimulatedInboxStore struct {
	db *sql.DB
}

func NewSimulatedInboxStore(db *sql.DB) *SimulatedInboxStore {
	return &SimulatedInboxStore{db: db}
}

func (s *SimulatedInboxStore) Insert(ctx context.Context, jobID, externalID string, data any) error {
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("sqlite: no se pudo serializar el registro simulado de SGP: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO sgp_simulated_inbox (job_id, external_id, data, inserted_at) VALUES (?, ?, ?, ?)
	`, jobID, externalID, string(dataJSON), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("sqlite: no se pudo insertar en sgp_simulated_inbox: %w", err)
	}
	return nil
}
