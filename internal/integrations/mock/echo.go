// Package mock contiene integraciones ficticias usadas para probar el
// núcleo de Nexus sin depender de un sistema externo real.
//
// EchoIntegration es una versión mínima pensada para validar el enrutamiento
// de la Fase 1 (Registry + handler send). La Fase 3 la amplía con latencia
// simulada y la posibilidad de forzar distintos resultados de error, para
// ejercitar también auditoría y logging — ver docs/10-plan-de-trabajo-poc.md.
package mock

import (
	"context"
	"encoding/json"

	"nexusgo/internal/core"
)

type EchoIntegration struct{}

func NewEchoIntegration() *EchoIntegration {
	return &EchoIntegration{}
}

func (i *EchoIntegration) Metadata() core.Metadata {
	return core.Metadata{
		ID:        "mock-echo",
		Name:      "Eco de prueba (sin sistema externo real)",
		Direction: core.DirectionOutbound,
		Mode:      core.ModeSync,
		Version:   "1.0",
	}
}

func (i *EchoIntegration) HandleSend(ctx context.Context, req core.SendRequest) (core.SendResult, error) {
	var payload any
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		return core.SendResult{}, core.NewValidationError("payload inválido: %w", err)
	}

	return core.SendResult{
		Status:  "SUCCESS",
		Data:    payload,
		Message: "eco de la solicitud (integración de prueba, sin sistema externo real)",
	}, nil
}
