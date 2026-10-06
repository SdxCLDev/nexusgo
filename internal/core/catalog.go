package core

import "context"

// CatalogEntry es una fila del catálogo de integraciones — ver
// docs/08-modelo-datos.md §8.2. Vive en core (no en internal/storage/sqlite)
// para que internal/api dependa solo de core y nunca de una capa de
// almacenamiento concreta.
type CatalogEntry struct {
	IntegrationID string
	Name          string
	Direction     string
	Mode          string
	Version       string
	Status        string
}

// CatalogStore expone el catálogo para el handler GET /integrations. La
// implementación de producción (internal/storage/sqlite) lo respalda en
// SQLite para reflejar el status persistido (ej. DISABLED); *Registry
// también la implementa devolviendo todo como ACTIVE, útil como catálogo de
// referencia y en pruebas sin base de datos de por medio.
type CatalogStore interface {
	Catalog(ctx context.Context) ([]CatalogEntry, error)
}

// Catalog implementa CatalogStore a partir de lo registrado en memoria.
func (r *Registry) Catalog(ctx context.Context) ([]CatalogEntry, error) {
	metas := r.List()
	entries := make([]CatalogEntry, 0, len(metas))
	for _, m := range metas {
		entries = append(entries, CatalogEntry{
			IntegrationID: m.ID,
			Name:          m.Name,
			Direction:     string(m.Direction),
			Mode:          string(m.Mode),
			Version:       m.Version,
			Status:        "ACTIVE",
		})
	}
	return entries, nil
}
