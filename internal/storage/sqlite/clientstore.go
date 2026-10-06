package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"nexusgo/internal/auth"
)

// ClientStore implementa auth.ClientStore sobre SQLite — ver
// docs/08-modelo-datos.md §8.1. Reemplaza a auth.InMemoryClientStore en
// producción (Fase 4); InMemoryClientStore se mantiene para pruebas rápidas
// del paquete internal/api.
type ClientStore struct {
	db *sql.DB
}

func NewClientStore(db *sql.DB) *ClientStore {
	return &ClientStore{db: db}
}

func (s *ClientStore) FindByID(ctx context.Context, id string) (auth.Client, bool, error) {
	var c auth.Client
	var scopesJSON string

	err := s.db.QueryRowContext(ctx,
		`SELECT client_id, api_key_hash, scopes, status FROM clients WHERE client_id = ?`, id,
	).Scan(&c.ID, &c.APIKeyHash, &scopesJSON, &c.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.Client{}, false, nil
	}
	if err != nil {
		return auth.Client{}, false, fmt.Errorf("sqlite: error buscando el cliente %q: %w", id, err)
	}

	if err := json.Unmarshal([]byte(scopesJSON), &c.Scopes); err != nil {
		return auth.Client{}, false, fmt.Errorf("sqlite: scopes corruptos para el cliente %q: %w", id, err)
	}
	return c, true, nil
}

// Upsert da de alta o reemplaza un cliente. Se usa para sembrar el cliente
// "sgp" al arrancar durante la PoC — ver docs/10-plan-de-trabajo-poc.md Fase 2/4.
func (s *ClientStore) Upsert(ctx context.Context, c auth.Client) error {
	scopesJSON, err := json.Marshal(c.Scopes)
	if err != nil {
		return fmt.Errorf("sqlite: no se pudo serializar los scopes de %q: %w", c.ID, err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO clients (client_id, api_key_hash, scopes, status, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(client_id) DO UPDATE SET
			api_key_hash = excluded.api_key_hash,
			scopes       = excluded.scopes,
			status       = excluded.status
	`, c.ID, c.APIKeyHash, string(scopesJSON), c.Status, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("sqlite: no se pudo guardar el cliente %q: %w", c.ID, err)
	}
	return nil
}
