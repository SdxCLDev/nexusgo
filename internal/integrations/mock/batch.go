package mock

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"nexusgo/internal/core"
	"nexusgo/internal/jobs"
)

// SimulatedInbox representa, solo para esta PoC, la base de datos externa
// (SGP) que recibe los registros cuando delivery_mode=push_db — ver
// docs/05-patron-asincrono.md §5.5(a). Cuando exista acceso real a SGP, esta
// simulación se reemplaza por internal/adapters/dbclient apuntando a la
// base de datos real.
type SimulatedInbox interface {
	Insert(ctx context.Context, jobID, externalID string, data any) error
}

// BatchIntegration simula una descarga masiva (ej. encabezados + detalle de
// minutas) desde un sistema externo, para validar el patrón asíncrono
// completo (docs/05-patron-asincrono.md) sin depender de AMD/SAP reales. Se
// registra dos veces en el Registry con distinto ID y delivery_mode (ver
// cmd/nexus/main.go) para ejercitar ambas modalidades de entrega con la
// misma lógica.
type BatchIntegration struct {
	id           string
	deliveryMode jobs.DeliveryMode
	inbox        SimulatedInbox
}

func NewBatchIntegration(id string, deliveryMode jobs.DeliveryMode, inbox SimulatedInbox) *BatchIntegration {
	return &BatchIntegration{id: id, deliveryMode: deliveryMode, inbox: inbox}
}

func (b *BatchIntegration) Metadata() core.Metadata {
	return core.Metadata{
		ID:             b.id,
		Name:           "Descarga por lotes de prueba (sin sistema externo real)",
		Direction:      core.DirectionInbound,
		Mode:           core.ModeAsync,
		Version:        "1.0",
		ExternalSystem: "MOCK",
		DeliveryMode:   b.deliveryMode,
	}
}

// HandleSend nunca debería invocarse: el núcleo enruta las integraciones
// ASYNC a Execute (ver internal/api/handlers/send.go). Se implementa solo
// para satisfacer core.Integration, que core.AsyncIntegration embebe.
func (b *BatchIntegration) HandleSend(ctx context.Context, req core.SendRequest) (core.SendResult, error) {
	return core.SendResult{}, core.NewInternalError("%q es una integración asíncrona; no debería invocarse vía HandleSend", b.id)
}

// batchControl son campos opcionales del payload para controlar la
// simulación (cantidad de ítems, fallas forzadas, latencia); no representan
// datos de negocio reales — mismo criterio que mock-echo (ver Fase 3).
type batchControl struct {
	TotalItems     int `json:"total_items,omitempty"`
	FailEvery      int `json:"fail_every,omitempty"` // ej. 3 => falla 1 de cada 3 ítems
	DelayPerItemMs int `json:"delay_per_item_ms,omitempty"`
}

func (b *BatchIntegration) Execute(run *jobs.Run) error {
	var control batchControl
	_ = json.Unmarshal(run.Payload, &control) // campos de control ausentes/inválidos se ignoran

	total := control.TotalItems
	if total <= 0 {
		total = 5
	}

	if err := run.ReportProgress(0, 0, &total); err != nil {
		return fmt.Errorf("no se pudo reportar el progreso inicial: %w", err)
	}

	attempted, failed := 0, 0
	for i := 1; i <= total; i++ {
		if control.DelayPerItemMs > 0 {
			select {
			case <-time.After(time.Duration(control.DelayPerItemMs) * time.Millisecond):
			case <-run.Context().Done():
				return run.Context().Err()
			}
		}

		externalID := fmt.Sprintf("MOCK-%04d", i)
		attempted++

		if control.FailEvery > 0 && i%control.FailEvery == 0 {
			failed++
			if err := run.AddItem(jobs.Item{
				ExternalID:  externalID,
				Status:      "FAILED",
				ErrorDetail: "fallo simulado (forzado por payload.fail_every)",
			}); err != nil {
				return fmt.Errorf("no se pudo registrar el ítem fallido %q: %w", externalID, err)
			}
		} else {
			data := map[string]any{"index": i, "external_id": externalID}
			if run.DeliveryMode == jobs.DeliveryPushDB {
				if err := b.inbox.Insert(run.Context(), run.JobID, externalID, data); err != nil {
					return fmt.Errorf("no se pudo insertar el ítem %q en el destino simulado: %w", externalID, err)
				}
			}
			if err := run.AddItem(jobs.Item{
				ExternalID: externalID,
				Status:     "SUCCESS",
				Data:       data,
			}); err != nil {
				return fmt.Errorf("no se pudo registrar el ítem exitoso %q: %w", externalID, err)
			}
		}

		if err := run.ReportProgress(attempted, failed, &total); err != nil {
			return fmt.Errorf("no se pudo reportar el progreso: %w", err)
		}
	}

	return nil
}
