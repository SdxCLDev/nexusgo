// Package jobmanager orquesta la ejecución de integraciones asíncronas:
// crea el Job, lo ejecuta en background con un límite de concurrencia
// configurable y actualiza su estado final — ver docs/05-patron-asincrono.md.
//
// Vive bajo internal/core porque orquesta core.AsyncIntegration, pero el
// tipo de datos del job vive en el paquete independiente internal/jobs para
// evitar un ciclo de importación — ver la nota en internal/jobs/jobs.go.
package jobmanager

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"nexusgo/internal/audit"
	"nexusgo/internal/core"
	"nexusgo/internal/jobs"
)

// Manager ejecuta jobs en background. El límite de concurrencia es el
// tamaño de un semáforo (channel con buffer): cada Submit lanza su propia
// goroutine, que se bloquea hasta que haya un cupo libre. Es una
// alternativa más simple que un worker pool clásico con cola explícita —
// suficiente para la PoC, ya que el propio job queda en PENDING mientras
// espera su turno.
type Manager struct {
	jobStore   jobs.Store
	auditStore audit.Store
	sem        chan struct{}
	logger     *slog.Logger
}

func NewManager(jobStore jobs.Store, auditStore audit.Store, concurrency int, logger *slog.Logger) *Manager {
	if concurrency <= 0 {
		concurrency = 1
	}
	return &Manager{
		jobStore:   jobStore,
		auditStore: auditStore,
		sem:        make(chan struct{}, concurrency),
		logger:     logger,
	}
}

// Submit crea el job en estado PENDING y lo encola para ejecución en
// background, devolviendo de inmediato — ver docs/05-patron-asincrono.md §5.3.
func (m *Manager) Submit(ctx context.Context, integration core.AsyncIntegration, correlationID, clientID string, payload []byte) (jobID string, err error) {
	meta := integration.Metadata()
	jobID = core.NewID()
	auditID := core.NewID()
	now := time.Now().UTC()

	if err := m.jobStore.Create(ctx, jobs.Job{
		ID:            jobID,
		IntegrationID: meta.ID,
		CorrelationID: correlationID,
		ClientID:      clientID,
		Status:        jobs.StatusPending,
		DeliveryMode:  meta.DeliveryMode,
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		return "", fmt.Errorf("jobmanager: no se pudo crear el job: %w", err)
	}

	if err := m.auditStore.Start(ctx, audit.Record{
		AuditID:        auditID,
		CorrelationID:  correlationID,
		JobID:          jobID,
		IntegrationID:  meta.ID,
		ClientID:       clientID,
		ExternalSystem: meta.ExternalSystem,
		Direction:      string(meta.Direction),
		Mode:           string(meta.Mode),
		RequestSummary: payload,
		StartedAt:      now,
	}); err != nil {
		m.logger.Error("no se pudo iniciar la auditoría del job", "job_id", jobID, "error", err)
	}

	go m.run(integration, jobID, auditID, correlationID, payload)

	return jobID, nil
}

func (m *Manager) run(integration core.AsyncIntegration, jobID, auditID, correlationID string, payload []byte) {
	m.sem <- struct{}{}
	defer func() { <-m.sem }()

	// El job sobrevive más allá del request HTTP que lo originó.
	ctx := context.Background()

	if err := m.jobStore.MarkRunning(ctx, jobID, time.Now().UTC()); err != nil {
		m.logger.Error("no se pudo marcar el job como RUNNING", "job_id", jobID, "error", err)
	}

	meta := integration.Metadata()
	run := jobs.NewRun(ctx, jobID, meta.ID, correlationID, payload, meta.DeliveryMode,
		func(processed, failed int, total *int) error {
			return m.jobStore.UpdateProgress(ctx, jobID, processed, failed, total)
		},
		func(item jobs.Item) error {
			if item.ID == "" {
				item.ID = core.NewID()
			}
			item.JobID = jobID
			item.CreatedAt = time.Now().UTC()
			return m.jobStore.AddItem(ctx, item)
		},
	)

	execErr := m.callExecute(integration, run)
	finishedAt := time.Now().UTC()

	if execErr != nil {
		m.logger.Error("job finalizado con error", "job_id", jobID, "error", execErr)
		if err := m.jobStore.Finish(ctx, jobID, jobs.StatusFailed, nil, finishedAt); err != nil {
			m.logger.Error("no se pudo finalizar el job", "job_id", jobID, "error", err)
		}
		if err := m.auditStore.Finish(ctx, auditID, audit.StatusFailed, nil, execErr.Error(), finishedAt); err != nil {
			m.logger.Error("no se pudo finalizar la auditoría del job", "audit_id", auditID, "error", err)
		}
		return
	}

	status := jobs.StatusCompleted
	var summary any
	if job, found, err := m.jobStore.Get(ctx, jobID); err != nil {
		m.logger.Error("no se pudo leer el job para calcular el resultado final", "job_id", jobID, "error", err)
	} else if found {
		summary = map[string]any{"total": job.ProgressTotal, "processed": job.ProgressProcessed, "failed": job.ProgressFailed}
		if job.ProgressFailed > 0 {
			status = jobs.StatusPartial
		}
	}

	if err := m.jobStore.Finish(ctx, jobID, status, summary, finishedAt); err != nil {
		m.logger.Error("no se pudo finalizar el job", "job_id", jobID, "error", err)
	}

	auditStatus := audit.StatusSuccess
	if status == jobs.StatusPartial {
		auditStatus = audit.StatusPartial
	}
	if err := m.auditStore.Finish(ctx, auditID, auditStatus, summary, "", finishedAt); err != nil {
		m.logger.Error("no se pudo finalizar la auditoría del job", "audit_id", auditID, "error", err)
	}
}

// callExecute recupera cualquier panic de la integración y lo convierte en
// error, igual que callIntegration en internal/api/handlers/send.go (ver
// Fase 3): sin esto, un panic dejaría el job en RUNNING para siempre.
func (m *Manager) callExecute(integration core.AsyncIntegration, run *jobs.Run) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic en la integración async: %v", r)
		}
	}()
	return integration.Execute(run)
}
