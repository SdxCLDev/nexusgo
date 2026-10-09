package amd

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"

	"nexusgo/internal/core"
	"nexusgo/internal/jobs"
)

// IntegrationIDDescargaMinuta es el integration_id estable de esta integración
// — ver la convención de nomenclatura en docs/09-guia-nueva-integracion.md §9.2.
const IntegrationIDDescargaMinuta = "amd-to-sgp-descarga-minuta"

// minutaSource abstrae las operaciones contra AMD que necesita la descarga.
// *Client la implementa; en pruebas permite inyectar un doble (incluso uno que
// entre en pánico, para verificar la resiliencia).
type minutaSource interface {
	GetMinutaHeaders(ctx context.Context) ([]MinutaHeader, error)
	GetMinutaDetail(ctx context.Context, idMinuta int) ([]MinutaDetalle, error)
}

// DescargaMinutaIntegration implementa core.AsyncIntegration: descarga desde
// AMD todas las minutas pendientes (encabezado + detalle por minuta), las
// valida y las deja disponibles para SGP. Es el caso de referencia del patrón
// asíncrono — ver docs/05-patron-asincrono.md §5.3.
type DescargaMinutaIntegration struct {
	source       minutaSource
	deliveryMode jobs.DeliveryMode
	concurrency  int
	logger       *slog.Logger
}

// NewDescargaMinutaIntegration construye la integración. concurrency limita
// cuántas consultas de detalle se hacen en paralelo contra AMD, para acelerar
// la descarga sin saturarlo — ver docs/05-patron-asincrono.md §5.8.
func NewDescargaMinutaIntegration(source minutaSource, deliveryMode jobs.DeliveryMode, concurrency int, logger *slog.Logger) *DescargaMinutaIntegration {
	if concurrency <= 0 {
		concurrency = 1
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &DescargaMinutaIntegration{source: source, deliveryMode: deliveryMode, concurrency: concurrency, logger: logger}
}

func (i *DescargaMinutaIntegration) Metadata() core.Metadata {
	return core.Metadata{
		ID:             IntegrationIDDescargaMinuta,
		Name:           "Descarga de minutas desde AMD",
		Direction:      core.DirectionInbound,
		Mode:           core.ModeAsync,
		Version:        "1.0",
		ExternalSystem: externalSystem,
		DeliveryMode:   i.deliveryMode,
	}
}

// HandleSend nunca debería invocarse: el núcleo enruta las integraciones ASYNC
// a Execute (ver internal/api/handlers/send.go). Se implementa solo para
// satisfacer core.Integration, que core.AsyncIntegration embebe.
func (i *DescargaMinutaIntegration) HandleSend(ctx context.Context, req core.SendRequest) (core.SendResult, error) {
	return core.SendResult{}, core.NewInternalError("%q es una integración asíncrona; se ejecuta vía Execute", IntegrationIDDescargaMinuta)
}

// Execute corre en background bajo el Job Manager. Obtiene los encabezados,
// descarga el detalle de cada minuta con un límite de concurrencia, valida y
// reporta cada minuta como un ítem del job (SUCCESS/FAILED). Una falla del
// paso inicial (autenticación o encabezados) aborta todo el job (FAILED);
// fallas de minutas individuales lo dejan PARTIAL — ver §5.9.
//
// Resiliencia: cada minuta se procesa en su propia goroutine, y cada goroutine
// recupera sus propios pánicos. La recuperación del Job Manager (callExecute)
// solo cubre la goroutine que ejecuta Execute, no las que esta integración
// lanza; sin esta protección, un pánico procesando una minuta tumbaría todo el
// proceso de Nexus. Un pánico se traduce en un ítem FAILED revisable.
func (i *DescargaMinutaIntegration) Execute(run *jobs.Run) error {
	ctx := run.Context()

	headers, err := i.source.GetMinutaHeaders(ctx)
	if err != nil {
		return core.NewExternalError("no se pudieron obtener los encabezados de minuta desde AMD: %w", err)
	}

	total := len(headers)
	if err := run.ReportProgress(0, 0, &total); err != nil {
		return fmt.Errorf("no se pudo reportar el progreso inicial: %w", err)
	}
	if total == 0 {
		return nil // no hay minutas pendientes: el job termina COMPLETED sin ítems.
	}

	var (
		mu        sync.Mutex
		processed int
		failed    int
		firstErr  error
	)
	// record registra un ítem y actualiza el progreso, seguro para uso
	// concurrente. El defer de Unlock libera el mutex aun si una escritura al
	// store entrara en pánico.
	record := func(item jobs.Item) {
		mu.Lock()
		defer mu.Unlock()
		processed++
		if item.Status == "FAILED" {
			failed++
		}
		if addErr := run.AddItem(item); addErr != nil && firstErr == nil {
			firstErr = fmt.Errorf("no se pudo registrar el ítem de la minuta %s: %w", item.ExternalID, addErr)
		}
		if pErr := run.ReportProgress(processed, failed, &total); pErr != nil && firstErr == nil {
			firstErr = fmt.Errorf("no se pudo reportar el progreso: %w", pErr)
		}
	}

	sem := make(chan struct{}, i.concurrency)
	var wg sync.WaitGroup

	for _, h := range headers {
		if ctx.Err() != nil {
			break // contexto cancelado: dejar de lanzar más trabajo.
		}
		wg.Add(1)
		go func(h MinutaHeader) {
			defer wg.Done()
			// Último resguardo: si la escritura del ítem al store entrara en
			// pánico (processMinuta ya es a prueba de pánicos), se registra en
			// el log y la goroutine termina sin tumbar el proceso.
			defer func() {
				if r := recover(); r != nil {
					i.logger.Error("pánico recuperado en worker de descarga de minuta",
						"integration_id", IntegrationIDDescargaMinuta, "id_minuta", h.IDMinuta, "panic", r)
				}
			}()

			sem <- struct{}{}
			defer func() { <-sem }()

			record(i.processMinuta(ctx, h))
		}(h)
	}
	wg.Wait()

	if ctx.Err() != nil {
		return ctx.Err()
	}
	return firstErr
}

// processMinuta descarga y valida una minuta, devolviendo el ítem a registrar.
// Nunca devuelve error ni entra en pánico: cualquier falla —incluido un pánico
// en la consulta a AMD o en la validación— se traduce en un ítem FAILED, para
// que el resto de la descarga continúe (resultado PARTIAL del job) y el motivo
// quede registrado y sea revisable.
func (i *DescargaMinutaIntegration) processMinuta(ctx context.Context, h MinutaHeader) (item jobs.Item) {
	externalID := strconv.Itoa(h.IDMinuta)

	defer func() {
		if r := recover(); r != nil {
			i.logger.Error("pánico recuperado procesando una minuta",
				"integration_id", IntegrationIDDescargaMinuta, "id_minuta", h.IDMinuta, "panic", r)
			item = jobs.Item{
				ExternalID:  externalID,
				Status:      "FAILED",
				ErrorDetail: fmt.Sprintf("error interno procesando la minuta: %v", r),
			}
		}
	}()

	detalle, err := i.source.GetMinutaDetail(ctx, h.IDMinuta)
	if err != nil {
		return jobs.Item{ExternalID: externalID, Status: "FAILED", ErrorDetail: err.Error()}
	}
	if verr := validate(h, detalle); verr != nil {
		return jobs.Item{ExternalID: externalID, Status: "FAILED", ErrorDetail: verr.Error()}
	}

	return jobs.Item{
		ExternalID: externalID,
		Status:     "SUCCESS",
		Data:       transform(h, detalle),
	}
}
