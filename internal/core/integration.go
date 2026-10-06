package core

import (
	"context"
	"encoding/json"

	"nexusgo/internal/jobs"
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

// AsyncIntegration es implementada por integraciones de modo ASYNC. El
// orquestador (internal/core/jobmanager) la invoca en background; Execute
// reporta progreso e ítems a través de run, sin bloquear el request HTTP
// original — ver docs/05-patron-asincrono.md.
type AsyncIntegration interface {
	Integration
	Execute(run *jobs.Run) error
}
