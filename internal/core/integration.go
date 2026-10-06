package core

import (
	"context"
	"encoding/json"
)

type SendRequest struct {
	CorrelationID string
	Payload       json.RawMessage
}

type SendResult struct {
	Status  string // "SUCCESS" | "PARTIAL" (los errores se devuelven como error, no aquí)
	Data    any
	Message string
}

// Integration es el contrato que toda integración (AMD, SAP, PEL, mock, ...)
// debe implementar para ser enrutada por el núcleo. Ver docs/02-arquitectura.md §2.3.
type Integration interface {
	Metadata() Metadata
	HandleSend(ctx context.Context, req SendRequest) (SendResult, error)
}

// AsyncIntegration y el ciclo de vida de Job se incorporan en la Fase 5
// (Job Manager) — ver docs/10-plan-de-trabajo-poc.md.
