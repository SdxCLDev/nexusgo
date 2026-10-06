package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nexusgo/internal/api"
	"nexusgo/internal/auth"
	"nexusgo/internal/core"
)

var testJWTSecret = []byte("test-secret")

const testTokenTTL = time.Minute

type stubIntegration struct {
	meta   core.Metadata
	result core.SendResult
	err    error
	calls  int
}

func (s *stubIntegration) Metadata() core.Metadata { return s.meta }

func (s *stubIntegration) HandleSend(ctx context.Context, req core.SendRequest) (core.SendResult, error) {
	s.calls++
	return s.result, s.err
}

func newClientStore(clients ...auth.Client) *auth.InMemoryClientStore {
	store := auth.NewInMemoryClientStore()
	for _, c := range clients {
		store.Upsert(c)
	}
	return store
}

func newTestRouter(t *testing.T, store auth.ClientStore, integrations ...core.Integration) http.Handler {
	t.Helper()
	reg := core.NewRegistry()
	for _, i := range integrations {
		reg.Register(i)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return api.NewRouter(logger, reg, store, testJWTSecret, testTokenTTL)
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
	store := newClientStore()
	router := newTestRouter(t, store, stub)
	token := testToken(t, "sgp", []string{"integration:stub-sync:invoke"})

	rec := doSend(t, router, token, "stub-sync", `{"source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{"a":1}}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	resp := decodeJSON(t, rec)
	if resp["correlation_id"] == "" || resp["correlation_id"] == nil {
		t.Error("se esperaba un correlation_id generado automáticamente")
	}
	if resp["status"] != "SUCCESS" {
		t.Errorf("status = %v, se esperaba SUCCESS", resp["status"])
	}
	if stub.calls != 1 {
		t.Errorf("HandleSend se llamó %d veces, se esperaba 1", stub.calls)
	}
}

func TestSend_Unauthorized_NoToken(t *testing.T) {
	stub := &stubIntegration{meta: core.Metadata{ID: "stub-sync", Mode: core.ModeSync}}
	router := newTestRouter(t, newClientStore(), stub)

	rec := doSend(t, router, "", "stub-sync", `{"source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{}}`)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if resp := decodeJSON(t, rec); resp["code"] != core.CodeUnauthorized {
		t.Errorf("code = %v, se esperaba %s", resp["code"], core.CodeUnauthorized)
	}
	if stub.calls != 0 {
		t.Error("HandleSend no debería invocarse sin autenticación")
	}
}

func TestSend_Unauthorized_InvalidToken(t *testing.T) {
	stub := &stubIntegration{meta: core.Metadata{ID: "stub-sync", Mode: core.ModeSync}}
	router := newTestRouter(t, newClientStore(), stub)

	rec := doSend(t, router, "esto-no-es-un-jwt", "stub-sync", `{"source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{}}`)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestSend_Forbidden_MissingScope(t *testing.T) {
	stub := &stubIntegration{meta: core.Metadata{ID: "stub-sync", Mode: core.ModeSync}}
	router := newTestRouter(t, newClientStore(), stub)
	token := testToken(t, "sgp", []string{"integration:otra-integracion:invoke"})

	rec := doSend(t, router, token, "stub-sync", `{"source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{}}`)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if resp := decodeJSON(t, rec); resp["code"] != core.CodeForbidden {
		t.Errorf("code = %v, se esperaba %s", resp["code"], core.CodeForbidden)
	}
	if stub.calls != 0 {
		t.Error("HandleSend no debería invocarse sin el scope requerido")
	}
}

func TestSend_IntegrationNotFound(t *testing.T) {
	router := newTestRouter(t, newClientStore())
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
	router := newTestRouter(t, newClientStore(), stub)
	token := testToken(t, "sgp", []string{"integration:stub-sync:invoke"})

	rec := doSend(t, router, token, "stub-sync", `{"timestamp":"2026-10-06T14:32:00Z","payload":{}}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if resp := decodeJSON(t, rec); resp["code"] != core.CodeInvalidEnvelope {
		t.Errorf("code = %v, se esperaba %s", resp["code"], core.CodeInvalidEnvelope)
	}
	if stub.calls != 0 {
		t.Error("HandleSend no debería invocarse si el envelope es inválido")
	}
}

func TestSend_AsyncNotYetSupported(t *testing.T) {
	stub := &stubIntegration{meta: core.Metadata{ID: "stub-async", Mode: core.ModeAsync}}
	router := newTestRouter(t, newClientStore(), stub)
	token := testToken(t, "sgp", []string{"integration:stub-async:invoke"})

	rec := doSend(t, router, token, "stub-async", `{"source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{}}`)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if stub.calls != 0 {
		t.Error("HandleSend no debería invocarse para una integración async (Job Manager pendiente, Fase 5)")
	}
}

func TestCatalog(t *testing.T) {
	stub := &stubIntegration{meta: core.Metadata{ID: "stub-sync", Name: "Stub", Direction: core.DirectionOutbound, Mode: core.ModeSync, Version: "1.0"}}
	router := newTestRouter(t, newClientStore(), stub)
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

func TestCatalog_RequiresAuthButNotScope(t *testing.T) {
	router := newTestRouter(t, newClientStore())

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
	router := newTestRouter(t, store, stub)

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
	router := newTestRouter(t, store)

	body, _ := json.Marshal(map[string]string{"client_id": "sgp", "api_key": "clave-incorrecta"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHealthAndReady(t *testing.T) {
	router := newTestRouter(t, newClientStore())

	for _, path := range []string{"/health", "/ready"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d", path, rec.Code)
		}
	}
}
