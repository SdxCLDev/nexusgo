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

	"nexusgo/internal/api"
	"nexusgo/internal/core"
)

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

func newTestRouter(t *testing.T, integrations ...core.Integration) http.Handler {
	t.Helper()
	reg := core.NewRegistry()
	for _, i := range integrations {
		reg.Register(i)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return api.NewRouter(logger, reg)
}

func doSend(t *testing.T, router http.Handler, integrationID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/"+integrationID+"/send", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestSend_Success(t *testing.T) {
	stub := &stubIntegration{
		meta:   core.Metadata{ID: "stub-sync", Direction: core.DirectionOutbound, Mode: core.ModeSync, Version: "1.0"},
		result: core.SendResult{Status: "SUCCESS", Data: map[string]any{"ok": true}},
	}
	router := newTestRouter(t, stub)

	rec := doSend(t, router, "stub-sync", `{"source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{"a":1}}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("respuesta no es JSON válido: %v", err)
	}
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

func TestSend_IntegrationNotFound(t *testing.T) {
	router := newTestRouter(t)

	rec := doSend(t, router, "no-existe", `{"source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{}}`)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["code"] != core.CodeIntegrationNotFound {
		t.Errorf("code = %v, se esperaba %s", resp["code"], core.CodeIntegrationNotFound)
	}
}

func TestSend_InvalidEnvelope(t *testing.T) {
	stub := &stubIntegration{meta: core.Metadata{ID: "stub-sync", Mode: core.ModeSync}}
	router := newTestRouter(t, stub)

	rec := doSend(t, router, "stub-sync", `{"timestamp":"2026-10-06T14:32:00Z","payload":{}}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["code"] != core.CodeInvalidEnvelope {
		t.Errorf("code = %v, se esperaba %s", resp["code"], core.CodeInvalidEnvelope)
	}
	if stub.calls != 0 {
		t.Error("HandleSend no debería invocarse si el envelope es inválido")
	}
}

func TestSend_AsyncNotYetSupported(t *testing.T) {
	stub := &stubIntegration{meta: core.Metadata{ID: "stub-async", Mode: core.ModeAsync}}
	router := newTestRouter(t, stub)

	rec := doSend(t, router, "stub-async", `{"source_system":"SGP","timestamp":"2026-10-06T14:32:00Z","payload":{}}`)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if stub.calls != 0 {
		t.Error("HandleSend no debería invocarse para una integración async (Job Manager pendiente, Fase 5)")
	}
}

func TestCatalog(t *testing.T) {
	stub := &stubIntegration{meta: core.Metadata{ID: "stub-sync", Name: "Stub", Direction: core.DirectionOutbound, Mode: core.ModeSync, Version: "1.0"}}
	router := newTestRouter(t, stub)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/integrations", nil)
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

func TestHealthAndReady(t *testing.T) {
	router := newTestRouter(t)

	for _, path := range []string{"/health", "/ready"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d", path, rec.Code)
		}
	}
}
