package jobs

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// InMemoryStore es una implementación en memoria de Store, usada en pruebas
// rápidas del paquete internal/api (ver auth.InMemoryClientStore y
// audit.InMemoryStore para el mismo patrón).
type InMemoryStore struct {
	mu    sync.Mutex
	jobs  map[string]*Job
	items map[string][]Item
}

func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{jobs: make(map[string]*Job), items: make(map[string][]Item)}
}

func (s *InMemoryStore) Create(ctx context.Context, job Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.jobs[job.ID]; exists {
		return fmt.Errorf("jobs: ya existe un job con id %q", job.ID)
	}
	cp := job
	s.jobs[job.ID] = &cp
	return nil
}

func (s *InMemoryStore) MarkRunning(ctx context.Context, jobID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[jobID]
	if !ok {
		return fmt.Errorf("jobs: el job %q no existe", jobID)
	}
	job.Status = StatusRunning
	job.UpdatedAt = at
	return nil
}

func (s *InMemoryStore) UpdateProgress(ctx context.Context, jobID string, processed, failed int, total *int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[jobID]
	if !ok {
		return fmt.Errorf("jobs: el job %q no existe", jobID)
	}
	job.ProgressProcessed = processed
	job.ProgressFailed = failed
	if total != nil {
		t := *total
		job.ProgressTotal = &t
	}
	job.UpdatedAt = time.Now().UTC()
	return nil
}

func (s *InMemoryStore) Finish(ctx context.Context, jobID string, status Status, resultSummary any, finishedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[jobID]
	if !ok {
		return fmt.Errorf("jobs: el job %q no existe", jobID)
	}
	if job.FinishedAt != nil {
		return fmt.Errorf("jobs: el job %q ya tiene estado final y es inmutable", jobID)
	}
	job.Status = status
	job.ResultSummary = resultSummary
	ft := finishedAt
	job.FinishedAt = &ft
	job.UpdatedAt = finishedAt
	return nil
}

func (s *InMemoryStore) Ack(ctx context.Context, jobID string, ackedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[jobID]
	if !ok {
		return fmt.Errorf("jobs: el job %q no existe", jobID)
	}
	at := ackedAt
	job.AckedAt = &at
	return nil
}

func (s *InMemoryStore) Get(ctx context.Context, jobID string) (Job, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[jobID]
	if !ok {
		return Job{}, false, nil
	}
	return *job, true, nil
}

func (s *InMemoryStore) AddItem(ctx context.Context, item Item) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[item.JobID] = append(s.items[item.JobID], item)
	return nil
}

func (s *InMemoryStore) ListItems(ctx context.Context, jobID string) ([]Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := s.items[jobID]
	result := make([]Item, len(items))
	copy(result, items)
	return result, nil
}

func (s *InMemoryStore) List(ctx context.Context, integrationID string, limit, offset int) ([]Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	filtered := make([]Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		if integrationID == "" || job.IntegrationID == integrationID {
			filtered = append(filtered, *job)
		}
	}
	// Más reciente primero; desempate estable por job_id.
	sort.Slice(filtered, func(i, j int) bool {
		if !filtered[i].CreatedAt.Equal(filtered[j].CreatedAt) {
			return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
		}
		return filtered[i].ID > filtered[j].ID
	})

	if offset >= len(filtered) {
		return []Job{}, nil
	}
	end := offset + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	return filtered[offset:end], nil
}
