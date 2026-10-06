package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"nexusgo/internal/core"
)

// CatalogStore implementa core.CatalogStore sobre SQLite — ver
// docs/08-modelo-datos.md §8.2.
type CatalogStore struct {
	db *sql.DB
}

func NewCatalogStore(db *sql.DB) *CatalogStore {
	return &CatalogStore{db: db}
}

// Sync inserta o actualiza la metadata de cada integración registrada en el
// Registry. Nunca toca la columna status de una fila ya existente, para no
// reactivar al reiniciar una integración deshabilitada administrativamente
// (el status se gestiona aparte; esta PoC no expone aún ese endpoint
// administrativo — ver docs/10-plan-de-trabajo-poc.md Fase 10).
func (s *CatalogStore) Sync(ctx context.Context, metas []core.Metadata) error {
	now := time.Now().UTC().Format(time.RFC3339)
	for _, m := range metas {
		_, err := s.db.ExecContext(ctx, `
			INSERT INTO integrations (integration_id, name, external_system, direction, mode, version, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, 'ACTIVE', ?, ?)
			ON CONFLICT(integration_id) DO UPDATE SET
				name            = excluded.name,
				external_system = excluded.external_system,
				direction       = excluded.direction,
				mode            = excluded.mode,
				version         = excluded.version,
				updated_at      = excluded.updated_at
		`, m.ID, m.Name, m.ExternalSystem, string(m.Direction), string(m.Mode), m.Version, now, now)
		if err != nil {
			return fmt.Errorf("sqlite: no se pudo sincronizar la integración %q: %w", m.ID, err)
		}
	}
	return nil
}

func (s *CatalogStore) Catalog(ctx context.Context) ([]core.CatalogEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT integration_id, name, direction, mode, version, status FROM integrations ORDER BY integration_id`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: no se pudo listar integrations: %w", err)
	}
	defer rows.Close()

	var entries []core.CatalogEntry
	for rows.Next() {
		var e core.CatalogEntry
		if err := rows.Scan(&e.IntegrationID, &e.Name, &e.Direction, &e.Mode, &e.Version, &e.Status); err != nil {
			return nil, fmt.Errorf("sqlite: error leyendo integrations: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
