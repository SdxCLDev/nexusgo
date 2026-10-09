package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nexusgo/internal/api"
	"nexusgo/internal/audit"
	"nexusgo/internal/auth"
	"nexusgo/internal/core"
	"nexusgo/internal/core/idempotency"
	"nexusgo/internal/core/jobmanager"
	"nexusgo/internal/jobs"
)

var testJWTSecret = []byte("test-secret")

const testTokenTTL = time.Minute
const testIdempotencyTTL = time.Hour

type stubIntegration struct {
	meta     core.Metadata
	result   core.SendResult
	err      error
	panicMsg string
	calls    int32
}

func (s *stubIntegration) Metadata() core.Metadata { return s.meta }

func (s *stubIntegration) HandleSend(ctx context.Context, req core.SendRequest) (core.SendResult, error) {
	atomic.AddInt32(&s.calls, 1)
	if s.panicMsg != "" {
		panic(s.panicMsg)
	}
	return s.result, s.err
}

func (s *stubIntegration) callCount() int32 { return atomic.LoadInt32(&s.calls) }

// blockingIntegration no responde hasta que se cierra release; se usa para
// probar el camino "solicitud en curso" (409) de la idempotencia.
type blockingIntegration struct {
	meta    core.Metadata
	started chan struct{}
	release chan struct{}
	calls   int32
}

func (b *blockingIntegration) Metadata() core.Metadata { return b.meta }

func (b *blockingIntegration) HandleSend(ctx context.Context, req core.SendRequest) (core.SendResult, error) {
	atomic.AddInt32(&b.calls, 1)
	close(b.started)
	<-b.release
	return core.SendResult{Status: "SUCCESS", Data: map[string]any{"ok": true}}, nil
}

// asyncStubIntegration permite a cada test controlar exactamente qué hace
// Execute (reportar progreso, agregar ítems, fallar, paniquear) sin depender
// de internal/integrations/mock (que tiene su propia suite de pruebas).
type asyncStubIntegration struct {
	meta    core.Metadata
	execute func(run *jobs.Run) error
	calls   int32
}

func (a *asyncStubIntegration) Metadata() core.Metadata { return a.meta }

func (a *asyncStubIntegration) HandleSend(ctx context.Context, req core.SendRequest) (core.SendResult, error) {
	return core.SendResult{}, core.NewInternalError("no debería invocarse: %q es asíncrona", a.meta.ID)
}

func (a *asyncStubIntegration) Execute(run *jobs.Run) error {
	atomic.AddInt32(&a.calls, 1)
	return a.execute(run)
}

// asyncNotAsyncIntegration está marcada ASYNC pero no implementa
// core.AsyncIntegration — simula un error de configuración.
type asyncNotAsyncIntegration struct {
	meta core.Metadata
}

func (a *asyncNotAsyncIntegration) Metadata() core.Metadata { return a.meta }

func (a *asyncNotAsyncIntegration) HandleSend(ctx context.Context, req core.SendRequest) (core.SendResult, error) {
	return core.SendResult{}, nil
}

func newClientStore(clients ...auth.Client) *auth.InMemoryClientStore {
	store := auth.NewInMemoryClientStore()
	for _, c := range clients {
		store.Upsert(c)
	}
	return store
}

func newTestRouter(t *testing.T, store auth.ClientStore, integrations ...core.Integration) (http.Handler, *audit.InMemoryStore) {
	t.Helper()
	router, auditStore, _ := newTestRouterWithJobs(t, store, integrations...)
	return router, auditStore
}

func newTestRouterWithJobs(t *testing.T, store auth.ClientStore, integrations ...core.Integration) (http.Handler, *audit.InMemoryStore, jobs.Store) {
	t.Helper()
	reg := core.NewRegistry()
	for _, i := range integrations {
		reg.Register(i)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditStore := audit.NewInMemoryStore()
	jobStore := jobs.NewInMemoryStore()
	jobManager := jobmanager.NewManager(jobStore, auditStore, 5, logger)
	router := api.NewRouter(api.Deps{
		Logger:       logger,
		Registry:     reg,
		CatalogStore: reg,
		ClientStore:  store,
		JWTSecret:    testJWTSecret,
		TokenTTL:     testTokenTTL,
		AuditStore:   auditStore,
		Idempotency:  idempotency.NewStore(testIdempotencyTTL),
		JobStore:     jobStore,
		JobManager:   jobManager,
	})
	return router, auditStore, jobStore
}

// newTestRouterWithBasePath es como newTestRouterWithJobs, pero con un
// BasePath configurado — para probar que status_url/result_url y el spec
// OpenAPI reflejan el prefijo de un reverse proxy (ver
// docs/10-plan-de-trabajo-poc.md, despliegue detrás de nginx en un sub-path).
func newTestRouterWithBasePath(t *testing.T, basePath string, integrations ...core.Integration) http.Handler {
	t.Helper()
	reg := core.NewRegistry()
	for _, i := range integrations {
		reg.Register(i)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditStore := audit.NewInMemoryStore()
	jobStore := jobs.NewInMemoryStore()
	jobManager := jobmanager.NewManager(jobStore, auditStore, 5, logger)
	return api.NewRouter(api.Deps{
		Logger:       logger,
		Registry:     reg,
		CatalogStore: reg,
		ClientStore:  newClientStore(),
		JWTSecret:    testJWTSecret,
		TokenTTL:     testTokenTTL,
		AuditStore:   auditStore,
		Idempotency:  idempotency.NewStore(testIdempotencyTTL),
		JobStore:     jobStore,
		JobManager:   jobManager,
		BasePath:     basePath,
	})
}

func testToken(t *testing.T, subject string, scopes []string) string {
	t.Helper()
	now := time.Now()
	token, err := auth.IssueJWT(testJWTSecret, auth.Claims{
		Subject:   subject,
		Scopes:    scopes,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(testTokenTTL).Unix(),
		Issuer:    "nexus",
	})
	if err != nil {
		t.Fatalf("no se pudo generar token de prueba: %v", err)
	}
	return token
}

func doSend(t *testing.T, router http.Handler, token, integrationID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/"+integrationID+"/send", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func doGet(t *testing.T, router http.Handler, token, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func doPost(t *testing.T, router http.Handler, token, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// waitForJobTerminal sondea GET /jobs/{job_id} hasta que el job alcance un
// estado terminal, igual que haría un cliente real (ver
// docs/05-patron-asincrono.md §5.3) — necesario porque Submit ejecuta el job
// en una goroutine en background.
func waitForJobTerminal(t *testing.T, router http.Handler, token, jobID string, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		rec := doGet(t, router, token, "/api/v1/jobs/"+jobID)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /jobs/%s: status = %d, body = %s", jobID, rec.Code, rec.Body.String())
		}
		resp := decodeJSON(t, rec)
		switch resp["status"] {
		case "COMPLETED", "FAILED", "PARTIAL":
			return resp
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout esperando que el job %s termine; último status=%v", jobID, resp["status"])
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("respuesta no es JSON válido: %v (body=%s)", err, rec.Body.String())
	}
	return resp
}

func TestSend_Success(t *testing.T) {
	stub := &stubIntegration{
		meta:   core.Metadata{ID: "stub-sync", Direction: core.DirectionOutbound, Mode: core.ModeSync, Version: "1.0"},
		result: core.SendResult{Status: "SUCCESS", Data: map[string]any{"ok": true}},
	}
	router, _ := newTestRouter(t, newClientStore(), stub)
	token := testToken(t, "sgp", []string{"integration:stub-sync:invoke"})

	rec := doSend(t, router, token, "stub-sync", `{"source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{"a":1}}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	resp := decodeJSON(t, rec)
	correlationID, _ := resp["correlation_id"].(string)
	if correlationID == "" {
		t.Error("se esperaba un correlation_id generado automáticamente")
	}
	if resp["status"] != "SUCCESS" {
		t.Errorf("status = %v, se esperaba SUCCESS", resp["status"])
	}
	if stub.callCount() != 1 {
		t.Errorf("HandleSend se llamó %d veces, se esperaba 1", stub.callCount())
	}
}

func findAuditByCorrelationID(t *testing.T, store *audit.InMemoryStore, correlationID string) *audit.Record {
	t.Helper()
	for _, rec := range store.All() {
		if rec.CorrelationID == correlationID {
			return &rec
		}
	}
	return nil
}

// TestSend_Success_WritesAudit verifica el contenido del registro de
// auditoría accediendo directamente al store (en producción no se expone
// audit_id al llamador, solo a través de un futuro endpoint administrativo).
func TestSend_Success_WritesAudit(t *testing.T) {
	stub := &stubIntegration{
		meta:   core.Metadata{ID: "stub-sync", Direction: core.DirectionOutbound, Mode: core.ModeSync, Version: "1.0", ExternalSystem: "TEST"},
		result: core.SendResult{Status: "SUCCESS", Data: map[string]any{"ok": true}},
	}
	router, auditStore := newTestRouter(t, newClientStore(), stub)
	token := testToken(t, "sgp", []string{"integration:stub-sync:invoke"})

	rec := doSend(t, router, token, "stub-sync", `{"correlation_id":"corr-audit-1","source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{"a":1}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	found := findAuditByCorrelationID(t, auditStore, "corr-audit-1")
	if found == nil {
		t.Fatal("no se encontró el registro de auditoría esperado")
	}
	if found.Status != audit.StatusSuccess {
		t.Errorf("status de auditoría = %v, se esperaba %v", found.Status, audit.StatusSuccess)
	}
	if found.IntegrationID != "stub-sync" || found.ClientID != "sgp" || found.ExternalSystem != "TEST" {
		t.Errorf("registro de auditoría inesperado: %+v", found)
	}
	if found.FinishedAt.IsZero() || found.DurationMs < 0 {
		t.Errorf("se esperaba FinishedAt/DurationMs calculados: %+v", found)
	}
}

func TestSend_Failure_WritesAuditFailed(t *testing.T) {
	stub := &stubIntegration{
		meta: core.Metadata{ID: "stub-sync", Mode: core.ModeSync},
		err:  core.NewExternalError("fallo simulado"),
	}
	router, auditStore := newTestRouter(t, newClientStore(), stub)
	token := testToken(t, "sgp", []string{"integration:stub-sync:invoke"})

	rec := doSend(t, router, token, "stub-sync", `{"correlation_id":"corr-audit-fail","source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{}}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	found := findAuditByCorrelationID(t, auditStore, "corr-audit-fail")
	if found == nil {
		t.Fatal("no se encontró el registro de auditoría esperado")
	}
	if found.Status != audit.StatusFailed {
		t.Errorf("status de auditoría = %v, se esperaba %v", found.Status, audit.StatusFailed)
	}
	if found.ErrorDetail == "" {
		t.Error("se esperaba ErrorDetail no vacío")
	}
}

func TestSend_PanicIsRecoveredAndDoesNotBlockRetry(t *testing.T) {
	stub := &stubIntegration{
		meta:     core.Metadata{ID: "stub-sync", Mode: core.ModeSync},
		panicMsg: "panic simulado",
	}
	router, auditStore := newTestRouter(t, newClientStore(), stub)
	token := testToken(t, "sgp", []string{"integration:stub-sync:invoke"})
	body := `{"correlation_id":"fixed-panic","source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{}}`

	rec := doSend(t, router, token, "stub-sync", body)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	resp := decodeJSON(t, rec)
	if resp["code"] != core.CodeInternalError {
		t.Errorf("code = %v, se esperaba %s", resp["code"], core.CodeInternalError)
	}
	if resp["correlation_id"] != "fixed-panic" {
		t.Errorf("correlation_id = %v, se esperaba que el handler lo conservara pese al panic", resp["correlation_id"])
	}

	found := findAuditByCorrelationID(t, auditStore, "fixed-panic")
	if found == nil {
		t.Fatal("se esperaba un registro de auditoría aun cuando la integración paniqueó")
	}
	if found.Status != audit.StatusFailed {
		t.Errorf("status de auditoría = %v, se esperaba %v (no debe quedar INICIADO para siempre)", found.Status, audit.StatusFailed)
	}

	// Un reintento con el mismo correlation_id no debe quedar bloqueado por
	// una clave de idempotencia huérfana.
	stub.err = nil
	stub.panicMsg = ""
	stub.result = core.SendResult{Status: "SUCCESS"}
	retryRec := doSend(t, router, token, "stub-sync", body)
	if retryRec.Code != http.StatusOK {
		t.Fatalf("reintento tras panic: status = %d, body = %s", retryRec.Code, retryRec.Body.String())
	}
}

func TestSend_Idempotency_ReplaySuccess(t *testing.T) {
	stub := &stubIntegration{
		meta:   core.Metadata{ID: "stub-sync", Mode: core.ModeSync},
		result: core.SendResult{Status: "SUCCESS", Data: map[string]any{"x": 1}},
	}
	router, _ := newTestRouter(t, newClientStore(), stub)
	token := testToken(t, "sgp", []string{"integration:stub-sync:invoke"})
	body := `{"correlation_id":"fixed-1","source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{"a":1}}`

	rec1 := doSend(t, router, token, "stub-sync", body)
	if rec1.Code != http.StatusOK {
		t.Fatalf("primera solicitud: status = %d, body = %s", rec1.Code, rec1.Body.String())
	}

	rec2 := doSend(t, router, token, "stub-sync", body)
	if rec2.Code != http.StatusOK {
		t.Fatalf("segunda solicitud (replay): status = %d, body = %s", rec2.Code, rec2.Body.String())
	}

	if stub.callCount() != 1 {
		t.Errorf("HandleSend se llamó %d veces, se esperaba 1 (la segunda debía ser un replay cacheado)", stub.callCount())
	}
	if decodeJSON(t, rec1)["correlation_id"] != decodeJSON(t, rec2)["correlation_id"] {
		t.Error("el correlation_id debería ser el mismo en el replay")
	}
}

func TestSend_Idempotency_FailureAllowsRetry(t *testing.T) {
	stub := &stubIntegration{
		meta: core.Metadata{ID: "stub-sync", Mode: core.ModeSync},
		err:  core.NewExternalError("fallo transitorio simulado"),
	}
	router, _ := newTestRouter(t, newClientStore(), stub)
	token := testToken(t, "sgp", []string{"integration:stub-sync:invoke"})
	body := `{"correlation_id":"fixed-retry","source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{}}`

	rec1 := doSend(t, router, token, "stub-sync", body)
	if rec1.Code != http.StatusBadGateway {
		t.Fatalf("primera solicitud: status = %d", rec1.Code)
	}

	rec2 := doSend(t, router, token, "stub-sync", body)
	if rec2.Code != http.StatusBadGateway {
		t.Fatalf("reintento: status = %d", rec2.Code)
	}

	if stub.callCount() != 2 {
		t.Errorf("HandleSend se llamó %d veces, se esperaba 2 (un fallo no debe cachearse)", stub.callCount())
	}
}

func TestSend_Idempotency_DuplicateInProgress(t *testing.T) {
	blocking := &blockingIntegration{
		meta:    core.Metadata{ID: "stub-blocking", Mode: core.ModeSync},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	router, _ := newTestRouter(t, newClientStore(), blocking)
	token := testToken(t, "sgp", []string{"integration:stub-blocking:invoke"})
	body := `{"correlation_id":"fixed-concurrent","source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{}}`

	var wg sync.WaitGroup
	var firstRec *httptest.ResponseRecorder
	wg.Add(1)
	go func() {
		defer wg.Done()
		firstRec = doSend(t, router, token, "stub-blocking", body)
	}()

	<-blocking.started
	secondRec := doSend(t, router, token, "stub-blocking", body)

	close(blocking.release)
	wg.Wait()

	if secondRec.Code != http.StatusConflict {
		t.Fatalf("segunda solicitud concurrente: status = %d, body = %s", secondRec.Code, secondRec.Body.String())
	}
	if resp := decodeJSON(t, secondRec); resp["code"] != core.CodeDuplicateRequest {
		t.Errorf("code = %v, se esperaba %s", resp["code"], core.CodeDuplicateRequest)
	}
	if firstRec.Code != http.StatusOK {
		t.Fatalf("primera solicitud: status = %d, body = %s", firstRec.Code, firstRec.Body.String())
	}
	if atomic.LoadInt32(&blocking.calls) != 1 {
		t.Errorf("HandleSend se llamó %d veces, se esperaba 1", blocking.calls)
	}

	// Una tercera solicitud, ya con el resultado completado, debe devolver
	// el eco cacheado sin volver a invocar HandleSend.
	thirdRec := doSend(t, router, token, "stub-blocking", body)
	if thirdRec.Code != http.StatusOK {
		t.Fatalf("tercera solicitud (replay): status = %d, body = %s", thirdRec.Code, thirdRec.Body.String())
	}
	if atomic.LoadInt32(&blocking.calls) != 1 {
		t.Errorf("HandleSend se llamó %d veces tras el replay, se esperaba que siguiera en 1", blocking.calls)
	}
}

func TestSend_Unauthorized_NoToken(t *testing.T) {
	stub := &stubIntegration{meta: core.Metadata{ID: "stub-sync", Mode: core.ModeSync}}
	router, _ := newTestRouter(t, newClientStore(), stub)

	rec := doSend(t, router, "", "stub-sync", `{"source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{}}`)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if resp := decodeJSON(t, rec); resp["code"] != core.CodeUnauthorized {
		t.Errorf("code = %v, se esperaba %s", resp["code"], core.CodeUnauthorized)
	}
	if stub.callCount() != 0 {
		t.Error("HandleSend no debería invocarse sin autenticación")
	}
}

func TestSend_Unauthorized_InvalidToken(t *testing.T) {
	stub := &stubIntegration{meta: core.Metadata{ID: "stub-sync", Mode: core.ModeSync}}
	router, _ := newTestRouter(t, newClientStore(), stub)

	rec := doSend(t, router, "esto-no-es-un-jwt", "stub-sync", `{"source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{}}`)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestSend_Forbidden_MissingScope(t *testing.T) {
	stub := &stubIntegration{meta: core.Metadata{ID: "stub-sync", Mode: core.ModeSync}}
	router, _ := newTestRouter(t, newClientStore(), stub)
	token := testToken(t, "sgp", []string{"integration:otra-integracion:invoke"})

	rec := doSend(t, router, token, "stub-sync", `{"source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{}}`)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if resp := decodeJSON(t, rec); resp["code"] != core.CodeForbidden {
		t.Errorf("code = %v, se esperaba %s", resp["code"], core.CodeForbidden)
	}
	if stub.callCount() != 0 {
		t.Error("HandleSend no debería invocarse sin el scope requerido")
	}
}

func TestSend_IntegrationNotFound(t *testing.T) {
	router, _ := newTestRouter(t, newClientStore())
	token := testToken(t, "sgp", nil)

	rec := doSend(t, router, token, "no-existe", `{"source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{}}`)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if resp := decodeJSON(t, rec); resp["code"] != core.CodeIntegrationNotFound {
		t.Errorf("code = %v, se esperaba %s", resp["code"], core.CodeIntegrationNotFound)
	}
}

func TestSend_InvalidEnvelope(t *testing.T) {
	stub := &stubIntegration{meta: core.Metadata{ID: "stub-sync", Mode: core.ModeSync}}
	router, _ := newTestRouter(t, newClientStore(), stub)
	token := testToken(t, "sgp", []string{"integration:stub-sync:invoke"})

	rec := doSend(t, router, token, "stub-sync", `{"timestamp":"2026-10-06T14:32:00Z","payload":{}}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if resp := decodeJSON(t, rec); resp["code"] != core.CodeInvalidEnvelope {
		t.Errorf("code = %v, se esperaba %s", resp["code"], core.CodeInvalidEnvelope)
	}
	if stub.callCount() != 0 {
		t.Error("HandleSend no debería invocarse si el envelope es inválido")
	}
}

func TestSend_Async_MisconfiguredIntegration(t *testing.T) {
	misconfigured := &asyncNotAsyncIntegration{meta: core.Metadata{ID: "stub-async-bad", Mode: core.ModeAsync}}
	router, _ := newTestRouter(t, newClientStore(), misconfigured)
	token := testToken(t, "sgp", []string{"integration:stub-async-bad:invoke"})

	rec := doSend(t, router, token, "stub-async-bad", `{"source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{}}`)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if resp := decodeJSON(t, rec); resp["code"] != core.CodeInternalError {
		t.Errorf("code = %v, se esperaba %s", resp["code"], core.CodeInternalError)
	}
}

func TestSend_Async_AcceptedAndCompletes(t *testing.T) {
	asyncInt := &asyncStubIntegration{
		meta: core.Metadata{
			ID: "stub-async", Mode: core.ModeAsync, Direction: core.DirectionInbound,
			ExternalSystem: "MOCK", DeliveryMode: jobs.DeliveryPullAPI,
		},
		execute: func(run *jobs.Run) error {
			total := 2
			if err := run.ReportProgress(0, 0, &total); err != nil {
				return err
			}
			if err := run.AddItem(jobs.Item{ExternalID: "A-1", Status: "SUCCESS", Data: map[string]any{"n": 1}}); err != nil {
				return err
			}
			if err := run.ReportProgress(1, 0, &total); err != nil {
				return err
			}
			if err := run.AddItem(jobs.Item{ExternalID: "A-2", Status: "SUCCESS", Data: map[string]any{"n": 2}}); err != nil {
				return err
			}
			return run.ReportProgress(2, 0, &total)
		},
	}
	router, auditStore, _ := newTestRouterWithJobs(t, newClientStore(), asyncInt)
	token := testToken(t, "sgp", []string{"integration:stub-async:invoke"})

	rec := doSend(t, router, token, "stub-async", `{"correlation_id":"corr-async-1","source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{}}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	accepted := decodeJSON(t, rec)
	jobID, _ := accepted["job_id"].(string)
	if jobID == "" {
		t.Fatal("se esperaba un job_id en la respuesta de aceptación")
	}
	if accepted["status"] != "ACCEPTED" || accepted["status_url"] != "/api/v1/jobs/"+jobID {
		t.Errorf("respuesta de aceptación inesperada: %+v", accepted)
	}

	final := waitForJobTerminal(t, router, token, jobID, 2*time.Second)
	if final["status"] != "COMPLETED" {
		t.Fatalf("status final = %v, se esperaba COMPLETED: %+v", final["status"], final)
	}
	progress, _ := final["progress"].(map[string]any)
	if progress["processed"] != float64(2) || progress["failed"] != float64(0) {
		t.Errorf("progress inesperado: %+v", progress)
	}
	if final["result_url"] != "/api/v1/jobs/"+jobID+"/result" {
		t.Errorf("result_url inesperado para delivery_mode pull_api: %+v", final)
	}

	resultRec := doGet(t, router, token, "/api/v1/jobs/"+jobID+"/result")
	if resultRec.Code != http.StatusOK {
		t.Fatalf("GET result: status = %d, body = %s", resultRec.Code, resultRec.Body.String())
	}
	result := decodeJSON(t, resultRec)
	summary, _ := result["summary"].(map[string]any)
	if summary["total"] != float64(2) || summary["success"] != float64(2) || summary["failed"] != float64(0) {
		t.Errorf("summary inesperado: %+v", summary)
	}
	items, _ := result["items"].([]any)
	if len(items) != 2 {
		t.Errorf("se esperaban 2 ítems, hay %d: %+v", len(items), items)
	}

	if atomic.LoadInt32(&asyncInt.calls) != 1 {
		t.Errorf("Execute se llamó %d veces, se esperaba 1", asyncInt.calls)
	}

	foundAudit := findAuditByCorrelationID(t, auditStore, "corr-async-1")
	if foundAudit == nil {
		t.Fatal("se esperaba un registro de auditoría para el job")
	}
	if foundAudit.JobID != jobID || foundAudit.Status != audit.StatusSuccess {
		t.Errorf("registro de auditoría del job inesperado: %+v", foundAudit)
	}

	ackRec := doPost(t, router, token, "/api/v1/jobs/"+jobID+"/ack", "")
	if ackRec.Code != http.StatusOK {
		t.Fatalf("ack: status = %d, body = %s", ackRec.Code, ackRec.Body.String())
	}
}

func TestSend_Async_PartialResult(t *testing.T) {
	asyncInt := &asyncStubIntegration{
		meta: core.Metadata{ID: "stub-async-partial", Mode: core.ModeAsync, DeliveryMode: jobs.DeliveryPullAPI},
		execute: func(run *jobs.Run) error {
			total := 2
			_ = run.ReportProgress(0, 0, &total)
			_ = run.AddItem(jobs.Item{ExternalID: "A-1", Status: "SUCCESS", Data: map[string]any{"n": 1}})
			_ = run.AddItem(jobs.Item{ExternalID: "A-2", Status: "FAILED", ErrorDetail: "fallo simulado"})
			return run.ReportProgress(2, 1, &total)
		},
	}
	router, _, _ := newTestRouterWithJobs(t, newClientStore(), asyncInt)
	token := testToken(t, "sgp", []string{"integration:stub-async-partial:invoke"})

	rec := doSend(t, router, token, "stub-async-partial", `{"source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{}}`)
	jobID := decodeJSON(t, rec)["job_id"].(string)

	final := waitForJobTerminal(t, router, token, jobID, 2*time.Second)
	if final["status"] != "PARTIAL" {
		t.Fatalf("status final = %v, se esperaba PARTIAL", final["status"])
	}

	result := decodeJSON(t, doGet(t, router, token, "/api/v1/jobs/"+jobID+"/result"))
	summary, _ := result["summary"].(map[string]any)
	if summary["success"] != float64(1) || summary["failed"] != float64(1) {
		t.Errorf("summary inesperado: %+v", summary)
	}
}

func TestSend_Async_ExecuteErrorMeansJobFailed(t *testing.T) {
	asyncInt := &asyncStubIntegration{
		meta: core.Metadata{ID: "stub-async-fail", Mode: core.ModeAsync, DeliveryMode: jobs.DeliveryPullAPI},
		execute: func(run *jobs.Run) error {
			return fmt.Errorf("no se pudo autenticar contra el sistema externo simulado")
		},
	}
	router, auditStore, _ := newTestRouterWithJobs(t, newClientStore(), asyncInt)
	token := testToken(t, "sgp", []string{"integration:stub-async-fail:invoke"})

	rec := doSend(t, router, token, "stub-async-fail", `{"correlation_id":"corr-async-fail","source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{}}`)
	jobID := decodeJSON(t, rec)["job_id"].(string)

	final := waitForJobTerminal(t, router, token, jobID, 2*time.Second)
	if final["status"] != "FAILED" {
		t.Fatalf("status final = %v, se esperaba FAILED", final["status"])
	}

	foundAudit := findAuditByCorrelationID(t, auditStore, "corr-async-fail")
	if foundAudit == nil || foundAudit.Status != audit.StatusFailed || foundAudit.ErrorDetail == "" {
		t.Errorf("registro de auditoría del job fallido inesperado: %+v", foundAudit)
	}
}

func TestSend_Async_PanicDoesNotLeaveJobRunningForever(t *testing.T) {
	asyncInt := &asyncStubIntegration{
		meta: core.Metadata{ID: "stub-async-panic", Mode: core.ModeAsync, DeliveryMode: jobs.DeliveryPullAPI},
		execute: func(run *jobs.Run) error {
			panic("panic simulado en Execute")
		},
	}
	router, _, _ := newTestRouterWithJobs(t, newClientStore(), asyncInt)
	token := testToken(t, "sgp", []string{"integration:stub-async-panic:invoke"})

	rec := doSend(t, router, token, "stub-async-panic", `{"source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{}}`)
	jobID := decodeJSON(t, rec)["job_id"].(string)

	final := waitForJobTerminal(t, router, token, jobID, 2*time.Second)
	if final["status"] != "FAILED" {
		t.Fatalf("status final = %v, se esperaba FAILED (panic recuperado)", final["status"])
	}
}

func TestJobResult_NotFinishedYet(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	asyncInt := &asyncStubIntegration{
		meta: core.Metadata{ID: "stub-async-slow", Mode: core.ModeAsync, DeliveryMode: jobs.DeliveryPullAPI},
		execute: func(run *jobs.Run) error {
			close(started)
			<-release
			return nil
		},
	}
	router, _, _ := newTestRouterWithJobs(t, newClientStore(), asyncInt)
	token := testToken(t, "sgp", []string{"integration:stub-async-slow:invoke"})

	rec := doSend(t, router, token, "stub-async-slow", `{"source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{}}`)
	jobID := decodeJSON(t, rec)["job_id"].(string)

	<-started
	resultRec := doGet(t, router, token, "/api/v1/jobs/"+jobID+"/result")
	if resultRec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", resultRec.Code, resultRec.Body.String())
	}
	if resp := decodeJSON(t, resultRec); resp["code"] != core.CodeJobNotFinished {
		t.Errorf("code = %v, se esperaba %s", resp["code"], core.CodeJobNotFinished)
	}

	close(release)
	waitForJobTerminal(t, router, token, jobID, 2*time.Second)
}

func TestJobStatus_NotFound(t *testing.T) {
	router, _ := newTestRouter(t, newClientStore())
	token := testToken(t, "sgp", nil)

	rec := doGet(t, router, token, "/api/v1/jobs/no-existe")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if resp := decodeJSON(t, rec); resp["code"] != core.CodeJobNotFound {
		t.Errorf("code = %v, se esperaba %s", resp["code"], core.CodeJobNotFound)
	}
}

func TestJobAck_NotFound(t *testing.T) {
	router, _ := newTestRouter(t, newClientStore())
	token := testToken(t, "sgp", nil)

	rec := doPost(t, router, token, "/api/v1/jobs/no-existe/ack", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestSend_Async_PushDBDeliveryMode(t *testing.T) {
	var sawDeliveryMode jobs.DeliveryMode
	asyncInt := &asyncStubIntegration{
		meta: core.Metadata{ID: "stub-async-push", Mode: core.ModeAsync, DeliveryMode: jobs.DeliveryPushDB},
		execute: func(run *jobs.Run) error {
			sawDeliveryMode = run.DeliveryMode
			return nil
		},
	}
	router, _, jobStore := newTestRouterWithJobs(t, newClientStore(), asyncInt)
	token := testToken(t, "sgp", []string{"integration:stub-async-push:invoke"})

	rec := doSend(t, router, token, "stub-async-push", `{"source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{}}`)
	jobID := decodeJSON(t, rec)["job_id"].(string)

	final := waitForJobTerminal(t, router, token, jobID, 2*time.Second)
	if sawDeliveryMode != jobs.DeliveryPushDB {
		t.Errorf("DeliveryMode visto por Execute = %v, se esperaba %v", sawDeliveryMode, jobs.DeliveryPushDB)
	}
	// Con delivery_mode push_db no se expone result_url: el resultado ya se
	// entregó directamente al destino simulado, no vía pull_api.
	if final["result_url"] != nil && final["result_url"] != "" {
		t.Errorf("no se esperaba result_url con delivery_mode push_db: %+v", final)
	}

	job, found, err := jobStore.Get(context.Background(), jobID)
	if err != nil || !found {
		t.Fatalf("se esperaba encontrar el job en el store: found=%v err=%v", found, err)
	}
	if job.DeliveryMode != jobs.DeliveryPushDB {
		t.Errorf("DeliveryMode persistido = %v, se esperaba %v", job.DeliveryMode, jobs.DeliveryPushDB)
	}
}

func TestJobList_PaginationAndFilter(t *testing.T) {
	asyncInt := &asyncStubIntegration{
		meta:    core.Metadata{ID: "stub-async-list", Mode: core.ModeAsync, DeliveryMode: jobs.DeliveryPullAPI},
		execute: func(run *jobs.Run) error { return nil },
	}
	router, _, _ := newTestRouterWithJobs(t, newClientStore(), asyncInt)
	token := testToken(t, "sgp", []string{"integration:stub-async-list:invoke"})

	for i := 0; i < 2; i++ {
		rec := doSend(t, router, token, "stub-async-list", `{"source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{}}`)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("submit %d: status = %d", i, rec.Code)
		}
		waitForJobTerminal(t, router, token, decodeJSON(t, rec)["job_id"].(string), 2*time.Second)
	}

	// Página 1 con limit=1: debe haber más.
	page1 := decodeJSON(t, doGet(t, router, token, "/api/v1/jobs?integration_id=stub-async-list&limit=1"))
	items1, _ := page1["items"].([]any)
	if len(items1) != 1 {
		t.Fatalf("página 1: se esperaba 1 ítem, hay %d", len(items1))
	}
	if page1["has_more"] != true {
		t.Errorf("página 1: se esperaba has_more=true")
	}
	cursor, _ := page1["next_cursor"].(string)
	if cursor == "" {
		t.Fatal("página 1: se esperaba next_cursor")
	}

	// Página 2 siguiendo el cursor: última página.
	page2 := decodeJSON(t, doGet(t, router, token, "/api/v1/jobs?integration_id=stub-async-list&limit=1&cursor="+cursor))
	items2, _ := page2["items"].([]any)
	if len(items2) != 1 {
		t.Fatalf("página 2: se esperaba 1 ítem, hay %d", len(items2))
	}
	if page2["has_more"] != false {
		t.Errorf("página 2: se esperaba has_more=false")
	}

	// Filtro por otra integración: vacío.
	page3 := decodeJSON(t, doGet(t, router, token, "/api/v1/jobs?integration_id=otra-integracion"))
	if items3, _ := page3["items"].([]any); len(items3) != 0 {
		t.Errorf("filtro por otra integración: se esperaban 0 ítems, hay %d", len(items3))
	}
}

func TestCatalog(t *testing.T) {
	stub := &stubIntegration{meta: core.Metadata{ID: "stub-sync", Name: "Stub", Direction: core.DirectionOutbound, Mode: core.ModeSync, Version: "1.0"}}
	router, _ := newTestRouter(t, newClientStore(), stub)
	token := testToken(t, "sgp", nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/integrations", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Integrations []map[string]any `json:"integrations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("respuesta no es JSON válido: %v", err)
	}
	if len(resp.Integrations) != 1 || resp.Integrations[0]["integration_id"] != "stub-sync" {
		t.Errorf("catálogo inesperado: %+v", resp.Integrations)
	}
}

func TestCatalog_RequiresAuth(t *testing.T) {
	router, _ := newTestRouter(t, newClientStore())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/integrations", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, se esperaba 401 sin token", rec.Code)
	}
}

func TestAuthToken_Success(t *testing.T) {
	store := newClientStore(auth.Client{
		ID:         "sgp",
		APIKeyHash: auth.HashAPIKey("clave-correcta"),
		Scopes:     []string{"integration:stub-sync:invoke"},
		Status:     auth.ClientStatusActive,
	})
	stub := &stubIntegration{
		meta:   core.Metadata{ID: "stub-sync", Mode: core.ModeSync},
		result: core.SendResult{Status: "SUCCESS"},
	}
	router, _ := newTestRouter(t, store, stub)

	body, _ := json.Marshal(map[string]string{"client_id": "sgp", "api_key": "clave-correcta"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	resp := decodeJSON(t, rec)
	token, _ := resp["access_token"].(string)
	if token == "" {
		t.Fatal("se esperaba un access_token no vacío")
	}

	// El token emitido debe servir para invocar la integración autorizada.
	sendRec := doSend(t, router, token, "stub-sync", `{"source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{}}`)
	if sendRec.Code != http.StatusOK {
		t.Fatalf("el token emitido no funcionó: status = %d, body = %s", sendRec.Code, sendRec.Body.String())
	}
}

func TestAuthToken_InvalidCredentials(t *testing.T) {
	store := newClientStore(auth.Client{
		ID:         "sgp",
		APIKeyHash: auth.HashAPIKey("clave-correcta"),
		Scopes:     nil,
		Status:     auth.ClientStatusActive,
	})
	router, _ := newTestRouter(t, store)

	body, _ := json.Marshal(map[string]string{"client_id": "sgp", "api_key": "clave-incorrecta"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestOpenAPIAndDocs(t *testing.T) {
	router, _ := newTestRouter(t, newClientStore())

	specRec := doGet(t, router, "", "/openapi.json")
	if specRec.Code != http.StatusOK {
		t.Fatalf("GET /openapi.json: status = %d", specRec.Code)
	}
	var spec map[string]any
	if err := json.Unmarshal(specRec.Body.Bytes(), &spec); err != nil {
		t.Fatalf("/openapi.json no es JSON válido: %v", err)
	}
	if spec["openapi"] == nil || spec["paths"] == nil {
		t.Errorf("spec OpenAPI con forma inesperada: %+v", spec)
	}

	docsRec := doGet(t, router, "", "/docs")
	if docsRec.Code != http.StatusOK {
		t.Fatalf("GET /docs: status = %d", docsRec.Code)
	}
	if ct := docsRec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type de /docs = %q", ct)
	}
}

func TestBasePath_ReflectedInUrls(t *testing.T) {
	asyncInt := &asyncStubIntegration{
		meta: core.Metadata{ID: "stub-async", Mode: core.ModeAsync, DeliveryMode: jobs.DeliveryPullAPI},
		execute: func(run *jobs.Run) error {
			return nil
		},
	}
	router := newTestRouterWithBasePath(t, "/nexus", asyncInt)
	token := testToken(t, "sgp", []string{"integration:stub-async:invoke"})

	rec := doSend(t, router, token, "stub-async", `{"source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{}}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	accepted := decodeJSON(t, rec)
	jobID, _ := accepted["job_id"].(string)
	wantStatusURL := "/nexus/api/v1/jobs/" + jobID
	if accepted["status_url"] != wantStatusURL {
		t.Errorf("status_url = %v, se esperaba %v", accepted["status_url"], wantStatusURL)
	}

	final := waitForJobTerminal(t, router, token, jobID, 2*time.Second)
	wantResultURL := "/nexus/api/v1/jobs/" + jobID + "/result"
	if final["result_url"] != wantResultURL {
		t.Errorf("result_url = %v, se esperaba %v", final["result_url"], wantResultURL)
	}

	spec := decodeJSON(t, doGet(t, router, "", "/openapi.json"))
	servers, _ := spec["servers"].([]any)
	if len(servers) != 1 {
		t.Fatalf("servers inesperado: %+v", spec["servers"])
	}
	server0, _ := servers[0].(map[string]any)
	if server0["url"] != "/nexus" {
		t.Errorf("servers[0].url = %v, se esperaba /nexus", server0["url"])
	}

	docsRec := doGet(t, router, "", "/docs")
	if !bytes.Contains(docsRec.Body.Bytes(), []byte(`/nexus/openapi.json`)) {
		t.Errorf("la página /docs no referencia /nexus/openapi.json: %s", docsRec.Body.String())
	}
}

func TestBasePath_EmptyMeansRootURLs(t *testing.T) {
	router := newTestRouterWithBasePath(t, "")

	spec := decodeJSON(t, doGet(t, router, "", "/openapi.json"))
	servers, _ := spec["servers"].([]any)
	server0, _ := servers[0].(map[string]any)
	if server0["url"] != "/" {
		t.Errorf("servers[0].url = %v, se esperaba \"/\" sin basePath", server0["url"])
	}
}

func TestHealthAndReady(t *testing.T) {
	router, _ := newTestRouter(t, newClientStore())

	for _, path := range []string{"/health", "/ready"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d", path, rec.Code)
		}
	}
}
