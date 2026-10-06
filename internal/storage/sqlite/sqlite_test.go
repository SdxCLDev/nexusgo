package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"nexusgo/internal/audit"
	"nexusgo/internal/auth"
	"nexusgo/internal/core"
	"nexusgo/internal/storage/sqlite"
)

func TestOpen_AppliesMigrationsIdempotently(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "nexus-test.db")

	db1, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("primera apertura: %v", err)
	}
	db1.Close()

	// Reabrir el mismo archivo no debe fallar al reaplicar migraciones ya
	// registradas en schema_migrations.
	db2, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("segunda apertura: %v", err)
	}
	defer db2.Close()

	var count int
	if err := db2.QueryRowContext(ctx, `SELECT COUNT(1) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("no se pudo leer schema_migrations: %v", err)
	}
	if count != 3 {
		t.Errorf("se esperaban 3 migraciones aplicadas, hay %d", count)
	}
}

func TestClientStore_UpsertAndFindByID(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "nexus-test.db")
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer db.Close()

	store := sqlite.NewClientStore(db)

	if _, found, err := store.FindByID(ctx, "sgp"); err != nil || found {
		t.Fatalf("no se esperaba encontrar el cliente todavía: found=%v err=%v", found, err)
	}

	client := auth.Client{
		ID:         "sgp",
		APIKeyHash: auth.HashAPIKey("clave"),
		Scopes:     []string{"integration:mock-echo:invoke", "integration:otra:invoke"},
		Status:     auth.ClientStatusActive,
	}
	if err := store.Upsert(ctx, client); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, found, err := store.FindByID(ctx, "sgp")
	if err != nil || !found {
		t.Fatalf("se esperaba encontrar el cliente: found=%v err=%v", found, err)
	}
	if got.APIKeyHash != client.APIKeyHash || got.Status != client.Status || len(got.Scopes) != 2 {
		t.Errorf("cliente recuperado inesperado: %+v", got)
	}

	// Upsert sobre el mismo client_id reemplaza los campos.
	client.Status = auth.ClientStatusRevoked
	if err := store.Upsert(ctx, client); err != nil {
		t.Fatalf("segundo Upsert: %v", err)
	}
	got, _, _ = store.FindByID(ctx, "sgp")
	if got.Status != auth.ClientStatusRevoked {
		t.Errorf("status = %v, se esperaba %v tras el upsert", got.Status, auth.ClientStatusRevoked)
	}
}

func TestCatalogStore_SyncAndCatalog(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "nexus-test.db")
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer db.Close()

	store := sqlite.NewCatalogStore(db)
	metas := []core.Metadata{
		{ID: "mock-echo", Name: "Eco", Direction: core.DirectionOutbound, Mode: core.ModeSync, Version: "1.0", ExternalSystem: "MOCK"},
	}
	if err := store.Sync(ctx, metas); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	entries, err := store.Catalog(ctx)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if len(entries) != 1 || entries[0].IntegrationID != "mock-echo" || entries[0].Status != "ACTIVE" {
		t.Fatalf("catálogo inesperado: %+v", entries)
	}

	// Deshabilitar manualmente (simulando una acción administrativa futura)
	// y verificar que un segundo Sync no la reactiva.
	if _, err := db.ExecContext(ctx, `UPDATE integrations SET status = 'DISABLED' WHERE integration_id = 'mock-echo'`); err != nil {
		t.Fatalf("no se pudo deshabilitar manualmente: %v", err)
	}
	if err := store.Sync(ctx, metas); err != nil {
		t.Fatalf("segundo Sync: %v", err)
	}
	entries, _ = store.Catalog(ctx)
	if entries[0].Status != "DISABLED" {
		t.Errorf("status = %v, Sync no debería reactivar una integración deshabilitada", entries[0].Status)
	}
}

func TestAuditStore_StartFinishAndImmutability(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "nexus-test.db")
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer db.Close()

	store := sqlite.NewAuditStore(db)
	startedAt := time.Now().UTC()

	if err := store.Start(ctx, audit.Record{
		AuditID:        "audit-1",
		CorrelationID:  "corr-1",
		IntegrationID:  "mock-echo",
		ClientID:       "sgp",
		ExternalSystem: "MOCK",
		Direction:      "OUTBOUND",
		Mode:           "SYNC",
		RequestSummary: map[string]any{"a": 1},
		StartedAt:      startedAt,
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	finishedAt := startedAt.Add(50 * time.Millisecond)
	if err := store.Finish(ctx, "audit-1", audit.StatusSuccess, map[string]any{"ok": true}, "", finishedAt); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	var status, errDetail string
	var durationMs int64
	if err := db.QueryRowContext(ctx,
		`SELECT status, COALESCE(error_detail, ''), duration_ms FROM audit_log WHERE audit_id = ?`, "audit-1",
	).Scan(&status, &errDetail, &durationMs); err != nil {
		t.Fatalf("no se pudo leer el registro: %v", err)
	}
	if status != string(audit.StatusSuccess) {
		t.Errorf("status = %v, se esperaba %v", status, audit.StatusSuccess)
	}
	if durationMs < 0 {
		t.Errorf("duration_ms inesperado: %d", durationMs)
	}

	// Inmutabilidad: un segundo Finish sobre el mismo audit_id debe fallar.
	if err := store.Finish(ctx, "audit-1", audit.StatusFailed, nil, "no debería aplicarse", finishedAt); err == nil {
		t.Error("se esperaba un error al intentar modificar un registro ya finalizado")
	}

	if err := store.Finish(ctx, "no-existe", audit.StatusSuccess, nil, "", finishedAt); err == nil {
		t.Error("se esperaba un error al finalizar un audit_id inexistente")
	}
}
