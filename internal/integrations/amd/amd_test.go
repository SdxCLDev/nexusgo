package amd

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"nexusgo/internal/adapters/restclient"
	"nexusgo/internal/credentials"
	"nexusgo/internal/jobs"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// --- Stub de la plataforma AMD ---

// amdStub emula el endpoint de autenticación y el de stored procedures de AMD.
// Es configurable por campos para ejercitar éxito, parcial, falla de auth y
// reautenticación ante 401.
type amdStub struct {
	server *httptest.Server

	mu          sync.Mutex
	logins      int
	getSPCalls  int
	headerCalls int
	detailCalls int

	headers []MinutaHeader
	detail  map[int][]MinutaDetalle

	loginStatus          int // HTTP status para /Login (0 => 200)
	unauthorizeFirstN    int // primeras N llamadas a getSP responden 401
	failDetailForMinutas map[int]bool
}

func newAMDStub(t *testing.T) *amdStub {
	t.Helper()
	s := &amdStub{detail: map[int][]MinutaDetalle{}, failDetailForMinutas: map[int]bool{}}
	s.server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.server.Close)
	return s
}

func (s *amdStub) handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case pathLogin:
		s.mu.Lock()
		s.logins++
		status := s.loginStatus
		login := s.logins
		s.mu.Unlock()
		if status != 0 && status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": 200,
			"result": map[string]any{
				"id":        16,
				"api_token": "tok-" + itoa(login),
			},
			"expiration": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		})

	case pathCallSP:
		s.mu.Lock()
		s.getSPCalls++
		unauth := s.getSPCalls <= s.unauthorizeFirstN
		s.mu.Unlock()
		if unauth {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get(tokenHeader) == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		var req spRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch req.Name {
		case spGetHeaders:
			s.mu.Lock()
			s.headerCalls++
			s.mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"result": s.headers, "status": "200"})
		case spGetDetail:
			id := paramInt(req.Parameters, "id")
			s.mu.Lock()
			s.detailCalls++
			fail := s.failDetailForMinutas[id]
			s.mu.Unlock()
			if fail {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"result": s.detail[id], "status": "200"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}

	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *amdStub) client() *Client {
	rest := restclient.New(s.server.URL,
		restclient.WithRetries(0, time.Millisecond),
		restclient.WithLogger(testLogger()),
	)
	creds := &stubCreds{found: true, cred: credentials.Credential{
		ExternalSystem: externalSystem, Type: credentials.TypeBasic, Environment: "DEV",
		Payload: map[string]string{"user": "admin", "password": "secreta"},
	}}
	return NewClient(rest, creds, "dev", testLogger())
}

// --- Dobles auxiliares ---

type stubCreds struct {
	cred  credentials.Credential
	found bool
}

func (s *stubCreds) Get(ctx context.Context, externalSystem, environment string) (credentials.Credential, bool, error) {
	return s.cred, s.found, nil
}
func (s *stubCreds) Upsert(ctx context.Context, c credentials.Credential) error { return nil }

// runRecorder captura los ítems y el progreso reportados por Execute, haciendo
// de sustituto del Job Manager en las pruebas.
type runRecorder struct {
	mu            sync.Mutex
	items         []jobs.Item
	lastProcessed int
	lastFailed    int
}

func (r *runRecorder) newRun(deliveryMode jobs.DeliveryMode) *jobs.Run {
	return jobs.NewRun(context.Background(), "job-test", IntegrationIDDescargaMinuta, "corr-test", []byte(`{}`), deliveryMode,
		func(processed, failed int, total *int) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.lastProcessed = processed
			r.lastFailed = failed
			return nil
		},
		func(item jobs.Item) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.items = append(r.items, item)
			return nil
		},
	)
}

func (r *runRecorder) byExternalID() map[string]jobs.Item {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]jobs.Item, len(r.items))
	for _, it := range r.items {
		out[it.ExternalID] = it
	}
	return out
}

// --- Pruebas ---

func TestExecute_Success(t *testing.T) {
	stub := newAMDStub(t)
	stub.headers = []MinutaHeader{{IDMinuta: 92032, Ceco: "73110"}, {IDMinuta: 92033, Ceco: "73110"}}
	stub.detail[92032] = []MinutaDetalle{{IDReceta: 14110, CantidadComensales: 100, Fecha: "2026-06-07T00:00:00"}}
	stub.detail[92033] = []MinutaDetalle{{IDReceta: 14111, CantidadComensales: 80, Fecha: "2026-06-14T00:00:00"}}

	integ := NewDescargaMinutaIntegration(stub.client(), jobs.DeliveryPullAPI, 2, testLogger())
	rec := &runRecorder{}

	if err := integ.Execute(rec.newRun(jobs.DeliveryPullAPI)); err != nil {
		t.Fatalf("Execute devolvió error: %v", err)
	}

	items := rec.byExternalID()
	if len(items) != 2 {
		t.Fatalf("se esperaban 2 ítems, hay %d", len(items))
	}
	for _, id := range []string{"92032", "92033"} {
		if items[id].Status != "SUCCESS" {
			t.Errorf("ítem %s: status = %s, se esperaba SUCCESS (%s)", id, items[id].Status, items[id].ErrorDetail)
		}
	}
	if rec.lastProcessed != 2 || rec.lastFailed != 0 {
		t.Errorf("progreso final processed=%d failed=%d, se esperaba 2/0", rec.lastProcessed, rec.lastFailed)
	}

	// El data del ítem debe ser la minuta transformada.
	result, ok := items["92032"].Data.(MinutaResult)
	if !ok {
		t.Fatalf("Data no es MinutaResult: %T", items["92032"].Data)
	}
	if result.IDMinuta != 92032 || len(result.Detalle) != 1 {
		t.Errorf("MinutaResult inesperado: %+v", result)
	}

	// Token cacheado: una sola autenticación pese a 1 consulta de encabezados + 2 de detalle.
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if stub.logins != 1 {
		t.Errorf("logins = %d, se esperaba 1 (token cacheado)", stub.logins)
	}
	if stub.detailCalls != 2 {
		t.Errorf("detailCalls = %d, se esperaba 2", stub.detailCalls)
	}
}

func TestExecute_PartialResult(t *testing.T) {
	stub := newAMDStub(t)
	stub.headers = []MinutaHeader{{IDMinuta: 1}, {IDMinuta: 2}, {IDMinuta: 3}}
	// 1: ok; 2: detalle vacío (falla validación); 3: AMD falla la consulta de detalle.
	stub.detail[1] = []MinutaDetalle{{IDReceta: 10, CantidadComensales: 50, Fecha: "2026-06-07"}}
	stub.detail[2] = []MinutaDetalle{}
	stub.failDetailForMinutas[3] = true

	integ := NewDescargaMinutaIntegration(stub.client(), jobs.DeliveryPullAPI, 3, testLogger())
	rec := &runRecorder{}

	if err := integ.Execute(rec.newRun(jobs.DeliveryPullAPI)); err != nil {
		t.Fatalf("Execute no debería devolver error en caso PARTIAL: %v", err)
	}

	items := rec.byExternalID()
	if items["1"].Status != "SUCCESS" {
		t.Errorf("minuta 1: se esperaba SUCCESS, got %s", items["1"].Status)
	}
	if items["2"].Status != "FAILED" || items["2"].ErrorDetail == "" {
		t.Errorf("minuta 2: se esperaba FAILED con detalle, got %+v", items["2"])
	}
	if items["3"].Status != "FAILED" || items["3"].ErrorDetail == "" {
		t.Errorf("minuta 3: se esperaba FAILED con detalle, got %+v", items["3"])
	}
	if rec.lastProcessed != 3 || rec.lastFailed != 2 {
		t.Errorf("progreso final processed=%d failed=%d, se esperaba 3/2", rec.lastProcessed, rec.lastFailed)
	}
}

func TestExecute_AuthFailureMeansJobError(t *testing.T) {
	stub := newAMDStub(t)
	stub.loginStatus = http.StatusUnauthorized // AMD rechaza la autenticación

	integ := NewDescargaMinutaIntegration(stub.client(), jobs.DeliveryPullAPI, 2, testLogger())
	rec := &runRecorder{}

	err := integ.Execute(rec.newRun(jobs.DeliveryPullAPI))
	if err == nil {
		t.Fatal("se esperaba error de Execute cuando falla la autenticación (job FAILED)")
	}
	if len(rec.items) != 0 {
		t.Errorf("no debería haber ítems si no se pudo autenticar: %+v", rec.items)
	}
}

func TestExecute_NoCredentialsConfigured(t *testing.T) {
	stub := newAMDStub(t)
	rest := restclient.New(stub.server.URL, restclient.WithRetries(0, time.Millisecond), restclient.WithLogger(testLogger()))
	client := NewClient(rest, &stubCreds{found: false}, "dev", testLogger())

	integ := NewDescargaMinutaIntegration(client, jobs.DeliveryPullAPI, 1, testLogger())
	if err := integ.Execute((&runRecorder{}).newRun(jobs.DeliveryPullAPI)); err == nil {
		t.Fatal("se esperaba error cuando no hay credenciales configuradas para AMD")
	}
}

// panickingSource simula una integración que explota al descargar el detalle,
// para verificar que un pánico en una goroutine worker no tumba el proceso.
type panickingSource struct {
	headers []MinutaHeader
}

func (p *panickingSource) GetMinutaHeaders(ctx context.Context) ([]MinutaHeader, error) {
	return p.headers, nil
}
func (p *panickingSource) GetMinutaDetail(ctx context.Context, idMinuta int) ([]MinutaDetalle, error) {
	panic("pánico simulado descargando el detalle")
}

func TestExecute_PanicInWorkerDoesNotCrashAndIsReviewable(t *testing.T) {
	src := &panickingSource{headers: []MinutaHeader{{IDMinuta: 1}, {IDMinuta: 2}}}
	integ := NewDescargaMinutaIntegration(src, jobs.DeliveryPullAPI, 2, testLogger())
	rec := &runRecorder{}

	// No debe propagar el pánico (si lo hiciera, este test —y en producción el
	// proceso— se caería).
	if err := integ.Execute(rec.newRun(jobs.DeliveryPullAPI)); err != nil {
		t.Fatalf("Execute no debe propagar el pánico de una minuta: %v", err)
	}

	items := rec.byExternalID()
	if len(items) != 2 {
		t.Fatalf("se esperaban 2 ítems (ambos FAILED), hay %d", len(items))
	}
	for _, id := range []string{"1", "2"} {
		if items[id].Status != "FAILED" || items[id].ErrorDetail == "" {
			t.Errorf("minuta %s: se esperaba FAILED con detalle revisable, got %+v", id, items[id])
		}
	}
	if rec.lastFailed != 2 {
		t.Errorf("failed=%d, se esperaba 2", rec.lastFailed)
	}
}

func TestGetMinutaHeaders_ReauthOn401(t *testing.T) {
	stub := newAMDStub(t)
	stub.headers = []MinutaHeader{{IDMinuta: 1}}
	stub.unauthorizeFirstN = 1 // la primera llamada a getSP devuelve 401

	client := stub.client()
	headers, err := client.GetMinutaHeaders(context.Background())
	if err != nil {
		t.Fatalf("GetMinutaHeaders falló pese a la reautenticación: %v", err)
	}
	if len(headers) != 1 {
		t.Fatalf("se esperaba 1 encabezado, hay %d", len(headers))
	}

	stub.mu.Lock()
	defer stub.mu.Unlock()
	if stub.logins != 2 {
		t.Errorf("logins = %d, se esperaba 2 (login inicial + reautenticación tras 401)", stub.logins)
	}
}

// --- helpers de test ---

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func paramInt(params []spParameter, text string) int {
	for _, p := range params {
		if p.Text == text {
			return atoi(p.Value)
		}
	}
	return 0
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
