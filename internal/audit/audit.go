// Package audit implementa el registro de auditoría de negocio — ver
// docs/07-logging-auditoria.md §7.2 y docs/08-modelo-datos.md §8.4.
package audit

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Status string

const (
	StatusStarted Status = "INICIADO"
	StatusSuccess Status = "EXITOSO"
	StatusFailed  Status = "FALLIDO"
	StatusPartial Status = "PARCIAL"
)

// Record es una fila de auditoría. Nace con Status=StatusStarted (vía
// Store.Start) y se completa una única vez con Store.Finish: una vez que
// Status llega a un valor terminal (EXITOSO/FALLIDO/PARCIAL) el registro es
// inmutable — ver docs/07-logging-auditoria.md §7.2.4.
type Record struct {
	AuditID         string
	CorrelationID   string
	JobID           string // vacío en operaciones síncronas; la Fase 5 lo completa.
	IntegrationID   string
	ClientID        string
	ExternalSystem  string
	Direction       string
	Mode            string
	Status          Status
	RequestSummary  any
	ResponseSummary any
	ErrorDetail     string
	StartedAt       time.Time
	FinishedAt      time.Time
	DurationMs      int64
}

type Store interface {
	Start(ctx context.Context, rec Record) error
	Finish(ctx context.Context, auditID string, status Status, responseSummary any, errDetail string, finishedAt time.Time) error
}

// InMemoryStore es la implementación de la PoC — se reemplaza por
// persistencia en SQLite en la Fase 4 (ver docs/10-plan-de-trabajo-poc.md).
type InMemoryStore struct {
	mu      sync.Mutex
	records map[string]*Record
}

func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{records: make(map[string]*Record)}
}

func (s *InMemoryStore) Start(ctx context.Context, rec Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.records[rec.AuditID]; exists {
		return fmt.Errorf("audit: ya existe un registro con audit_id %q", rec.AuditID)
	}
	cp := rec
	cp.Status = StatusStarted
	s.records[rec.AuditID] = &cp
	return nil
}

func (s *InMemoryStore) Finish(ctx context.Context, auditID string, status Status, responseSummary any, errDetail string, finishedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.records[auditID]
	if !ok {
		return fmt.Errorf("audit: el registro %q no existe", auditID)
	}
	if !rec.FinishedAt.IsZero() {
		return fmt.Errorf("audit: el registro %q ya tiene estado final (%s) y es inmutable", auditID, rec.Status)
	}

	rec.Status = status
	rec.ResponseSummary = responseSummary
	rec.ErrorDetail = errDetail
	rec.FinishedAt = finishedAt
	rec.DurationMs = finishedAt.Sub(rec.StartedAt).Milliseconds()
	return nil
}

// Get expone un registro para pruebas/depuración durante la PoC. No forma
// parte del contrato público (ver nota sobre endpoint administrativo en
// docs/07-logging-auditoria.md §7.2.6).
func (s *InMemoryStore) Get(auditID string) (Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[auditID]
	if !ok {
		return Record{}, false
	}
	return *rec, true
}

// All devuelve una copia de todos los registros — solo para
// pruebas/depuración durante la PoC (ver nota en Get).
func (s *InMemoryStore) All() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Record, 0, len(s.records))
	for _, rec := range s.records {
		result = append(result, *rec)
	}
	return result
}
