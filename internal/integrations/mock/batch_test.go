package mock_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"nexusgo/internal/core"
	"nexusgo/internal/integrations/mock"
	"nexusgo/internal/jobs"
)

// fakeInbox registra las inserciones simuladas sin tocar una base de datos
// real — ver mock.SimulatedInbox.
type fakeInbox struct {
	mu     sync.Mutex
	rows   []string
	failOn string
}

func (f *fakeInbox) Insert(ctx context.Context, jobID, externalID string, data any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if externalID == f.failOn {
		return context.DeadlineExceeded
	}
	f.rows = append(f.rows, externalID)
	return nil
}

type progressCall struct {
	processed, failed int
	total             *int
}

func newRun(ctx context.Context, payload []byte, deliveryMode jobs.DeliveryMode) (*jobs.Run, *[]progressCall, *[]jobs.Item) {
	var progress []progressCall
	var items []jobs.Item
	run := jobs.NewRun(ctx, "job-1", "mock-batch-pull", "corr-1", payload, deliveryMode,
		func(processed, failed int, total *int) error {
			progress = append(progress, progressCall{processed, failed, total})
			return nil
		},
		func(item jobs.Item) error {
			items = append(items, item)
			return nil
		},
	)
	return run, &progress, &items
}

func TestBatchIntegration_DefaultTotalAllSuccess(t *testing.T) {
	inbox := &fakeInbox{}
	integration := mock.NewBatchIntegration("mock-batch-pull", jobs.DeliveryPullAPI, inbox)
	run, progress, items := newRun(context.Background(), []byte(`{}`), jobs.DeliveryPullAPI)

	if err := integration.Execute(run); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(*items) != 5 { // default total_items
		t.Fatalf("se esperaban 5 ítems por defecto, hay %d", len(*items))
	}
	for _, it := range *items {
		if it.Status != "SUCCESS" {
			t.Errorf("ítem inesperado como fallido: %+v", it)
		}
	}
	last := (*progress)[len(*progress)-1]
	if last.processed != 5 || last.failed != 0 {
		t.Errorf("progreso final inesperado: %+v", last)
	}
	if len(inbox.rows) != 0 {
		t.Errorf("pull_api no debería insertar en el inbox simulado, insertó %v", inbox.rows)
	}
}

func TestBatchIntegration_CustomTotalAndFailEvery(t *testing.T) {
	inbox := &fakeInbox{}
	integration := mock.NewBatchIntegration("mock-batch-pull", jobs.DeliveryPullAPI, inbox)
	run, _, items := newRun(context.Background(), []byte(`{"total_items":6,"fail_every":3}`), jobs.DeliveryPullAPI)

	if err := integration.Execute(run); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(*items) != 6 {
		t.Fatalf("se esperaban 6 ítems, hay %d", len(*items))
	}
	failed := 0
	for _, it := range *items {
		if it.Status == "FAILED" {
			failed++
			if it.ErrorDetail == "" {
				t.Errorf("se esperaba error_detail en el ítem fallido %+v", it)
			}
		}
	}
	if failed != 2 { // ítems 3 y 6 (cada 3)
		t.Errorf("se esperaban 2 ítems fallidos, hay %d", failed)
	}
}

func TestBatchIntegration_PushDBInsertsOnlySuccessfulItems(t *testing.T) {
	inbox := &fakeInbox{}
	integration := mock.NewBatchIntegration("mock-batch-push", jobs.DeliveryPushDB, inbox)
	run, _, items := newRun(context.Background(), []byte(`{"total_items":4,"fail_every":2}`), jobs.DeliveryPushDB)

	if err := integration.Execute(run); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(inbox.rows) != 2 { // ítems 1 y 3 son éxito; 2 y 4 fallan (fail_every=2)
		t.Fatalf("se esperaban 2 filas insertadas en el inbox simulado, hay %d: %v", len(inbox.rows), inbox.rows)
	}
	successInItems := 0
	for _, it := range *items {
		if it.Status == "SUCCESS" {
			successInItems++
		}
	}
	if successInItems != len(inbox.rows) {
		t.Errorf("cantidad de ítems SUCCESS (%d) no coincide con filas insertadas (%d)", successInItems, len(inbox.rows))
	}
}

func TestBatchIntegration_RespectsContextCancellation(t *testing.T) {
	integration := mock.NewBatchIntegration("mock-batch-pull", jobs.DeliveryPullAPI, &fakeInbox{})
	ctx, cancel := context.WithCancel(context.Background())
	run, _, _ := newRun(ctx, []byte(`{"total_items":100,"delay_per_item_ms":50}`), jobs.DeliveryPullAPI)

	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	err := integration.Execute(run)
	if err == nil {
		t.Fatal("se esperaba un error por cancelación del contexto")
	}
}

func TestBatchIntegration_HandleSendIsNeverExpectedToRun(t *testing.T) {
	integration := mock.NewBatchIntegration("mock-batch-pull", jobs.DeliveryPullAPI, &fakeInbox{})
	_, err := integration.HandleSend(context.Background(), core.SendRequest{})
	if err == nil {
		t.Error("se esperaba un error al invocar HandleSend sobre una integración asíncrona")
	}
}
