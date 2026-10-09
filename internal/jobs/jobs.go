// Package jobs modela el ciclo de vida de una ejecución asíncrona — ver
// docs/05-patron-asincrono.md y docs/08-modelo-datos.md §8.3.
//
// Es un paquete independiente (no vive dentro de internal/core) a propósito:
// internal/core necesita referenciar el tipo Run en la firma de
// AsyncIntegration.Execute, mientras que el orquestador
// (internal/core/jobmanager) necesita referenciar core.AsyncIntegration para
// invocarlo. Si Job/Run vivieran dentro de core, core y jobmanager se
// importarían mutuamente. Con jobs como paquete aparte: core importa jobs
// (para AsyncIntegration), y jobmanager importa tanto core como jobs — sin
// ciclos. Ver la nota correspondiente en docs/10-plan-de-trabajo-poc.md Fase 1/5.
package jobs

import (
	"context"
	"time"
)

type Status string

const (
	StatusPending   Status = "PENDING"
	StatusRunning   Status = "RUNNING"
	StatusCompleted Status = "COMPLETED"
	StatusFailed    Status = "FAILED"
	StatusPartial   Status = "PARTIAL"
)

type DeliveryMode string

const (
	DeliveryPullAPI DeliveryMode = "pull_api"
	DeliveryPushDB  DeliveryMode = "push_db"
)

// Job es el ciclo de vida persistido de una ejecución asíncrona — ver
// docs/08-modelo-datos.md §8.3.
type Job struct {
	ID                string
	IntegrationID     string
	CorrelationID     string
	ClientID          string
	Status            Status
	DeliveryMode      DeliveryMode
	ProgressTotal     *int
	ProgressProcessed int
	ProgressFailed    int
	ResultSummary     any
	CreatedAt         time.Time
	UpdatedAt         time.Time
	FinishedAt        *time.Time
	AckedAt           *time.Time
}

// Item es el detalle por elemento de un job — ver docs/08-modelo-datos.md §8.3.1.
type Item struct {
	ID          string
	JobID       string
	ExternalID  string
	Status      string // "SUCCESS" | "FAILED"
	Data        any
	ErrorDetail string
	CreatedAt   time.Time
}

// Store persiste jobs y sus ítems. InMemoryStore (este paquete) se usa en
// pruebas; internal/storage/sqlite.JobStore respalda producción.
type Store interface {
	Create(ctx context.Context, job Job) error
	MarkRunning(ctx context.Context, jobID string, at time.Time) error
	UpdateProgress(ctx context.Context, jobID string, processed, failed int, total *int) error
	Finish(ctx context.Context, jobID string, status Status, resultSummary any, finishedAt time.Time) error
	Ack(ctx context.Context, jobID string, ackedAt time.Time) error
	Get(ctx context.Context, jobID string) (Job, bool, error)
	AddItem(ctx context.Context, item Item) error
	ListItems(ctx context.Context, jobID string) ([]Item, error)
	// List devuelve jobs ordenados del más reciente al más antiguo, filtrados
	// por integrationID si no está vacío, paginados con limit/offset — ver
	// docs/03-contrato-api-rest.md §3.10. Se usa para descubrir descargas
	// pasadas sin conocer el job_id de antemano.
	List(ctx context.Context, integrationID string, limit, offset int) ([]Job, error)
}

// Run es el handle que una AsyncIntegration recibe mientras procesa un job,
// para reportar progreso e ítems sin conocer el almacenamiento subyacente ni
// al orquestador — ver docs/02-arquitectura.md §2.3. Se construye con NewRun
// (los closures los arma internal/core/jobmanager).
type Run struct {
	JobID         string
	IntegrationID string
	CorrelationID string
	Payload       []byte // equivalente a json.RawMessage, sin importar encoding/json en este paquete
	DeliveryMode  DeliveryMode

	ctx            context.Context
	reportProgress func(processed, failed int, total *int) error
	addItem        func(item Item) error
}

func NewRun(
	ctx context.Context,
	jobID, integrationID, correlationID string,
	payload []byte,
	deliveryMode DeliveryMode,
	reportProgress func(processed, failed int, total *int) error,
	addItem func(item Item) error,
) *Run {
	return &Run{
		ctx:            ctx,
		JobID:          jobID,
		IntegrationID:  integrationID,
		CorrelationID:  correlationID,
		Payload:        payload,
		DeliveryMode:   deliveryMode,
		reportProgress: reportProgress,
		addItem:        addItem,
	}
}

func (r *Run) Context() context.Context { return r.ctx }

func (r *Run) ReportProgress(processed, failed int, total *int) error {
	return r.reportProgress(processed, failed, total)
}

func (r *Run) AddItem(item Item) error {
	return r.addItem(item)
}
