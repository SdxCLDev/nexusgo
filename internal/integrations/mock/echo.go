// Package mock contiene integraciones ficticias usadas para probar el
// núcleo de Nexus sin depender de un sistema externo real.
package mock

import (
	"context"
	"encoding/json"
	"time"

	"nexusgo/internal/core"
)

// EchoIntegration transforma y devuelve el payload recibido, simulando
// latencia y distintos resultados vía campos de control opcionales del
// propio payload (ver controlFields), para poder ejercitar todos los
// caminos de error del contrato (docs/03-contrato-api-rest.md §3.8) y la
// auditoría/logging sin un sistema externo real.
type EchoIntegration struct{}

func NewEchoIntegration() *EchoIntegration {
	return &EchoIntegration{}
}

func (i *EchoIntegration) Metadata() core.Metadata {
	return core.Metadata{
		ID:             "mock-echo",
		Name:           "Eco de prueba (sin sistema externo real)",
		Direction:      core.DirectionOutbound,
		Mode:           core.ModeSync,
		Version:        "1.0",
		ExternalSystem: "MOCK",
	}
}

// controlFields son campos opcionales que el payload puede incluir para
// forzar el comportamiento de la simulación; no representan datos de
// negocio reales.
type controlFields struct {
	DelayMs int    `json:"delay_ms,omitempty"`
	Force   string `json:"force,omitempty"` // "", "success", "business_error", "external_error", "panic"
}

func (i *EchoIntegration) HandleSend(ctx context.Context, req core.SendRequest) (core.SendResult, error) {
	var payload any
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		return core.SendResult{}, core.NewValidationError("payload inválido: %w", err)
	}

	var control controlFields
	_ = json.Unmarshal(req.Payload, &control) // campos de control ausentes/no numéricos se ignoran

	if control.DelayMs > 0 {
		select {
		case <-time.After(time.Duration(control.DelayMs) * time.Millisecond):
		case <-ctx.Done():
			return core.SendResult{}, core.NewExternalError("solicitud cancelada durante la simulación de latencia: %w", ctx.Err())
		}
	}

	switch control.Force {
	case "", "success":
		return core.SendResult{
			Status:  "SUCCESS",
			Data:    payload,
			Message: "eco de la solicitud (integración de prueba, sin sistema externo real)",
		}, nil
	case "business_error":
		return core.SendResult{}, core.NewBusinessRuleError("regla de negocio simulada rechazada (forzado por payload.force)")
	case "external_error":
		return core.SendResult{}, core.NewExternalError("fallo del sistema externo simulado (forzado por payload.force)")
	case "panic":
		panic("panic simulado (forzado por payload.force) — valida el middleware de recuperación")
	default:
		return core.SendResult{}, core.NewValidationError("valor de 'force' no reconocido: %q", control.Force)
	}
}
