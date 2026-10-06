package core

import (
	"encoding/json"
	"time"
)

// Envelope es el sobre genérico de solicitud — ver docs/03-contrato-api-rest.md §3.4.
type Envelope struct {
	CorrelationID string          `json:"correlation_id"`
	SourceSystem  string          `json:"source_system"`
	Timestamp     time.Time       `json:"timestamp"`
	Payload       json.RawMessage `json:"payload"`
}

// Validate verifica los campos obligatorios del envelope. correlation_id no
// es obligatorio aquí: si viene vacío, Nexus lo genera (ver NewID).
func (e *Envelope) Validate() *Error {
	if e.SourceSystem == "" {
		return NewEnvelopeError("el campo 'source_system' es obligatorio")
	}
	if e.Timestamp.IsZero() {
		return NewEnvelopeError("el campo 'timestamp' es obligatorio y debe ser RFC 3339")
	}
	if len(e.Payload) == 0 {
		return NewEnvelopeError("el campo 'payload' es obligatorio")
	}
	return nil
}
