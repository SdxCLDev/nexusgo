package api_test

import (
	"bytes"
	"context"
	"encoding/json"
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

func newClientStore(clients ...auth.Client) *auth.InMemoryClientStore {
	store := auth.NewInMemoryClientStore()
	for _, c := range clients {
		store.Upsert(c)
	}
	return store
}

func newTestRouter(t *testing.T, store auth.ClientStore, integrations ...core.Integration) (http.Handler, *audit.InMemoryStore) {
	t.Helper()
	reg := core.NewRegistry()
	for _, i := range integrations {
		reg.Register(i)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditStore := audit.NewInMemoryStore()
	router := api.NewRouter(api.Deps{
		Logger:       logger,
		Registry:     reg,
		CatalogStore: reg,
		ClientStore:  store,
		JWTSecret:    testJWTSecret,
		TokenTTL:     testTokenTTL,
		AuditStore:   auditStore,
		Idempotency:  idempotency.NewStore(testIdempotencyTTL),
	})
	return router, auditStore
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

func TestSend_AsyncNotYetSupported(t *testing.T) {
	stub := &stubIntegration{meta: core.Metadata{ID: "stub-async", Mode: core.ModeAsync}}
	router, _ := newTestRouter(t, newClientStore(), stub)
	token := testToken(t, "sgp", []string{"integration:stub-async:invoke"})

	rec := doSend(t, router, token, "stub-async", `{"source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{}}`)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if stub.callCount() != 0 {
		t.Error("HandleSend no debería invocarse para una integración async (Job Manager pendiente, Fase 5)")
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
