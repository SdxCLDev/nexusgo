package core

import (
	"fmt"
	"sort"
	"sync"
)

// Registry asocia cada integration_id con su implementación — ver
// docs/02-arquitectura.md §2.4.
type Registry struct {
	mu    sync.RWMutex
	items map[string]Integration
}

func NewRegistry() *Registry {
	return &Registry{items: make(map[string]Integration)}
}

// Register da de alta una integración. Entra en pánico ante un ID duplicado
// o vacío: es un error de programación detectado al arrancar el proceso,
// no una condición a manejar en tiempo de ejecución.
func (r *Registry) Register(integration Integration) {
	meta := integration.Metadata()
	if meta.ID == "" {
		panic("core: Integration.Metadata().ID no puede ser vacío")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.items[meta.ID]; exists {
		panic(fmt.Sprintf("core: integración ya registrada: %q", meta.ID))
	}
	r.items[meta.ID] = integration
}

func (r *Registry) Get(id string) (Integration, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	integration, ok := r.items[id]
	return integration, ok
}

// List devuelve la metadata de todas las integraciones registradas,
// ordenada por integration_id para una salida estable.
func (r *Registry) List() []Metadata {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Metadata, 0, len(r.items))
	for _, integration := range r.items {
		result = append(result, integration.Metadata())
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
