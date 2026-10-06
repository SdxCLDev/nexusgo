package jobmanager_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"nexusgo/internal/audit"
	"nexusgo/internal/core"
	"nexusgo/internal/core/jobmanager"
	"nexusgo/internal/jobs"
)

// asyncStub es un core.AsyncIntegration mínimo para probar el Manager de
// forma aislada, sin pasar por la capa HTTP (ver internal/api/router_test.go
// para las pruebas end-to-end del mismo flujo).
type asyncStub struct {
	meta    core.Metadata
	execute func(run *jobs.Run) error
}

func (a *asyncStub) Metadata() core.Metadata { return a.meta }

func (a *asyncStub) HandleSend(ctx context.Context, req core.SendRequest) (core.SendResult, error) {
	return core.SendResult{}, core.NewInternalError("no debería invocarse")
}

func (a *asyncStub) Execute(run *jobs.Run) error { return a.execute(run) }

func newManager(t *testing.T) (*jobmanager.Manager, jobs.Store, *audit.InMemoryStore) {
	t.Helper()
	jobStore := jobs.NewInMemoryStore()
	auditStore := audit.NewInMemoryStore()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return jobmanager.NewManager(jobStore, auditStore, 3, logger), jobStore, auditStore
}

func waitTerminal(t *testing.T, store jobs.Store, jobID string, timeout time.Duration) jobs.Job {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(timeout)
	for {
		job, found, err := store.Get(ctx, jobID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if found && job.FinishedAt != nil {
			return job
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout esperando que el job %s termine", jobID)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func findAuditByJobID(store *audit.InMemoryStore, jobID string) *audit.Record {
	for _, rec := range store.All() {
		if rec.JobID == jobID {
			r := rec
			return &r
		}
	}
	return nil
}

func TestManager_Submit_CompletesSuccessfully(t *testing.T) {
	mgr, jobStore, auditStore := newManager(t)
	integration := &asyncStub{
		meta: core.Metadata{ID: "stub", ExternalSystem: "MOCK", Direction: core.DirectionInbound, Mode: core.ModeAsync},
		execute: func(run *jobs.Run) error {
			total := 2
			if err := run.ReportProgress(0, 0, &total); err != nil {
				return err
			}
			if err := run.AddItem(jobs.Item{ExternalID: "1", Status: "SUCCESS"}); err != nil {
				return err
			}
			return run.ReportProgress(1, 0, &total)
		},
	}

	jobID, err := mgr.Submit(context.Background(), integration, "corr-1", "sgp", []byte(`{}`))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	job := waitTerminal(t, jobStore, jobID, time.Second)
	if job.Status != jobs.StatusCompleted {
		t.Errorf("status = %v, se esperaba %v", job.Status, jobs.StatusCompleted)
	}
	if job.ProgressProcessed != 1 || job.ProgressFailed != 0 {
		t.Errorf("progreso inesperado: %+v", job)
	}

	items, err := jobStore.ListItems(context.Background(), jobID)
	if err != nil || len(items) != 1 {
		t.Errorf("ListItems: items=%v err=%v", items, err)
	}

	rec := findAuditByJobID(auditStore, jobID)
	if rec == nil || rec.Status != audit.StatusSuccess || rec.CorrelationID != "corr-1" {
		t.Errorf("registro de auditoría inesperado: %+v", rec)
	}
}

func TestManager_Submit_PartialWhenItemsFail(t *testing.T) {
	mgr, jobStore, _ := newManager(t)
	integration := &asyncStub{
		meta: core.Metadata{ID: "stub", Mode: core.ModeAsync},
		execute: func(run *jobs.Run) error {
			total := 2
			_ = run.AddItem(jobs.Item{ExternalID: "1", Status: "SUCCESS"})
			_ = run.AddItem(jobs.Item{ExternalID: "2", Status: "FAILED", ErrorDetail: "x"})
			return run.ReportProgress(2, 1, &total)
		},
	}

	jobID, err := mgr.Submit(context.Background(), integration, "corr-2", "sgp", []byte(`{}`))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	job := waitTerminal(t, jobStore, jobID, time.Second)
	if job.Status != jobs.StatusPartial {
		t.Errorf("status = %v, se esperaba %v", job.Status, jobs.StatusPartial)
	}
}

func TestManager_Submit_ExecuteErrorMeansFailed(t *testing.T) {
	mgr, jobStore, auditStore := newManager(t)
	integration := &asyncStub{
		meta: core.Metadata{ID: "stub", Mode: core.ModeAsync},
		execute: func(run *jobs.Run) error {
			return fmt.Errorf("fallo irrecuperable simulado")
		},
	}

	jobID, err := mgr.Submit(context.Background(), integration, "corr-3", "sgp", []byte(`{}`))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	job := waitTerminal(t, jobStore, jobID, time.Second)
	if job.Status != jobs.StatusFailed {
		t.Errorf("status = %v, se esperaba %v", job.Status, jobs.StatusFailed)
	}

	rec := findAuditByJobID(auditStore, jobID)
	if rec == nil || rec.Status != audit.StatusFailed || rec.ErrorDetail == "" {
		t.Errorf("registro de auditoría inesperado: %+v", rec)
	}
}

func TestManager_Submit_PanicDoesNotHangJob(t *testing.T) {
	mgr, jobStore, _ := newManager(t)
	integration := &asyncStub{
		meta: core.Metadata{ID: "stub", Mode: core.ModeAsync},
		execute: func(run *jobs.Run) error {
			panic("panic simulado")
		},
	}

	jobID, err := mgr.Submit(context.Background(), integration, "corr-4", "sgp", []byte(`{}`))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	job := waitTerminal(t, jobStore, jobID, time.Second)
	if job.Status != jobs.StatusFailed {
		t.Errorf("status = %v, se esperaba %v (panic recuperado)", job.Status, jobs.StatusFailed)
	}
}

func TestManager_ConcurrencyLimit(t *testing.T) {
	jobStore := jobs.NewInMemoryStore()
	auditStore := audit.NewInMemoryStore()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := jobmanager.NewManager(jobStore, auditStore, 1, logger) // concurrencia 1: fuerza serialización

	release := make(chan struct{})
	var running int32
	maxConcurrent := make(chan int32, 10)

	makeIntegration := func(id string) *asyncStub {
		return &asyncStub{
			meta: core.Metadata{ID: id, Mode: core.ModeAsync},
			execute: func(run *jobs.Run) error {
				running++
				maxConcurrent <- running
				<-release
				running--
				return nil
			},
		}
	}

	jobID1, err := mgr.Submit(context.Background(), makeIntegration("stub-1"), "corr-c1", "sgp", []byte(`{}`))
	if err != nil {
		t.Fatalf("Submit 1: %v", err)
	}
	jobID2, err := mgr.Submit(context.Background(), makeIntegration("stub-2"), "corr-c2", "sgp", []byte(`{}`))
	if err != nil {
		t.Fatalf("Submit 2: %v", err)
	}

	<-maxConcurrent // el primer job ya está corriendo
	select {
	case n := <-maxConcurrent:
		t.Fatalf("con concurrencia=1 no debería haber un segundo job corriendo en paralelo (running=%d)", n)
	case <-time.After(50 * time.Millisecond):
		// esperado: el segundo job sigue esperando su turno
	}

	close(release)
	waitTerminal(t, jobStore, jobID1, time.Second)
	waitTerminal(t, jobStore, jobID2, time.Second)
}
