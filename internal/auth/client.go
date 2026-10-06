package auth

import (
	"context"
	"sync"
)

const (
	ClientStatusActive  = "ACTIVE"
	ClientStatusRevoked = "REVOKED"
)

// Client representa a un consumidor autorizado de Nexus — ver
// docs/08-modelo-datos.md §8.1.
type Client struct {
	ID         string
	APIKeyHash string
	Scopes     []string
	Status     string
}

// ClientStore abstrae el almacenamiento de clientes. La Fase 2 usa
// InMemoryClientStore; la Fase 4 lo reemplaza por una implementación
// respaldada en SQLite sin cambiar esta interfaz ni sus consumidores.
type ClientStore interface {
	FindByID(ctx context.Context, id string) (Client, bool, error)
}

type InMemoryClientStore struct {
	mu      sync.RWMutex
	clients map[string]Client
}

func NewInMemoryClientStore() *InMemoryClientStore {
	return &InMemoryClientStore{clients: make(map[string]Client)}
}

// Upsert da de alta o reemplaza un cliente. Pensado para la carga inicial
// (seed) al arrancar el proceso durante la PoC.
func (s *InMemoryClientStore) Upsert(c Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients[c.ID] = c
}

func (s *InMemoryClientStore) FindByID(ctx context.Context, id string) (Client, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.clients[id]
	return c, ok, nil
}
