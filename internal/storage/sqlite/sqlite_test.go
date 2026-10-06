package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"nexusgo/internal/audit"
	"nexusgo/internal/auth"
	"nexusgo/internal/core"
	"nexusgo/internal/jobs"
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
	const expectedMigrations = 6 // clients, integrations, audit_log, jobs, job_items, sgp_simulated_inbox
	if count != expectedMigrations {
		t.Errorf("se esperaban %d migraciones aplicadas, hay %d", expectedMigrations, count)
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

func TestJobStore_LifecycleAndItems(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "nexus-test.db")
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer db.Close()

	store := sqlite.NewJobStore(db)
	now := time.Now().UTC()

	if err := store.Create(ctx, jobs.Job{
		ID:            "job-1",
		IntegrationID: "mock-batch-pull",
		CorrelationID: "corr-1",
		ClientID:      "sgp",
		Status:        jobs.StatusPending,
		DeliveryMode:  jobs.DeliveryPullAPI,
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	job, found, err := store.Get(ctx, "job-1")
	if err != nil || !found {
		t.Fatalf("se esperaba encontrar el job: found=%v err=%v", found, err)
	}
	if job.Status != jobs.StatusPending {
		t.Errorf("status = %v, se esperaba %v", job.Status, jobs.StatusPending)
	}

	if err := store.MarkRunning(ctx, "job-1", now); err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}
	if err := store.MarkRunning(ctx, "job-1", now); err == nil {
		t.Error("un segundo MarkRunning sobre un job que ya no está PENDING debería fallar")
	}

	total := 3
	if err := store.UpdateProgress(ctx, "job-1", 2, 1, &total); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}

	if err := store.AddItem(ctx, jobs.Item{ID: "item-1", JobID: "job-1", ExternalID: "MOCK-0001", Status: "SUCCESS", Data: map[string]any{"x": 1}, CreatedAt: now}); err != nil {
		t.Fatalf("AddItem (success): %v", err)
	}
	if err := store.AddItem(ctx, jobs.Item{ID: "item-2", JobID: "job-1", ExternalID: "MOCK-0002", Status: "FAILED", ErrorDetail: "fallo simulado", CreatedAt: now}); err != nil {
		t.Fatalf("AddItem (failed): %v", err)
	}

	items, err := store.ListItems(ctx, "job-1")
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("se esperaban 2 ítems, hay %d", len(items))
	}
	byExternalID := make(map[string]jobs.Item, len(items))
	for _, it := range items {
		byExternalID[it.ExternalID] = it
	}
	if byExternalID["MOCK-0002"].ErrorDetail != "fallo simulado" {
		t.Errorf("error_detail inesperado: %+v", byExternalID["MOCK-0002"])
	}
	if byExternalID["MOCK-0001"].Status != "SUCCESS" {
		t.Errorf("status inesperado: %+v", byExternalID["MOCK-0001"])
	}

	finishedAt := now.Add(100 * time.Millisecond)
	if err := store.Finish(ctx, "job-1", jobs.StatusPartial, map[string]any{"total": 3}, finishedAt); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	job, _, _ = store.Get(ctx, "job-1")
	if job.Status != jobs.StatusPartial || job.FinishedAt == nil {
		t.Errorf("job tras Finish inesperado: %+v", job)
	}
	if job.ProgressTotal == nil || *job.ProgressTotal != 3 {
		t.Errorf("progress_total inesperado: %+v", job.ProgressTotal)
	}

	// Inmutabilidad, igual que audit_log.
	if err := store.Finish(ctx, "job-1", jobs.StatusFailed, nil, finishedAt); err == nil {
		t.Error("se esperaba un error al intentar modificar un job ya finalizado")
	}

	if err := store.Ack(ctx, "job-1", finishedAt); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	job, _, _ = store.Get(ctx, "job-1")
	if job.AckedAt == nil {
		t.Error("se esperaba acked_at seteado tras Ack")
	}

	if _, found, err := store.Get(ctx, "no-existe"); err != nil || found {
		t.Errorf("no se esperaba encontrar un job inexistente: found=%v err=%v", found, err)
	}
}

func TestSimulatedInboxStore_Insert(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "nexus-test.db")
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer db.Close()

	store := sqlite.NewSimulatedInboxStore(db)
	if err := store.Insert(ctx, "job-1", "MOCK-0001", map[string]any{"index": 1}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM sgp_simulated_inbox WHERE job_id = ? AND external_id = ?`, "job-1", "MOCK-0001").Scan(&count); err != nil {
		t.Fatalf("no se pudo verificar la inserción: %v", err)
	}
	if count != 1 {
		t.Errorf("se esperaba 1 fila insertada, hay %d", count)
	}
}
