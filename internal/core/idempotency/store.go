// Package idempotency implementa la deduplicación por correlation_id descrita
// en docs/03-contrato-api-rest.md §3.9: una solicitud en curso responde 409,
// y una ya completada con éxito devuelve el resultado cacheado sin volver a
// ejecutar la integración. Las solicitudes que terminaron en error NO se
// cachean: según docs/04-patron-sincrono.md §4.6, SGP debe poder reintentar
// con el mismo correlation_id tras un 502/503.
package idempotency

import (
	"sync"
	"time"

	"nexusgo/internal/core"
)

type entry struct {
	inProgress bool
	result     core.SendResult
	expiresAt  time.Time
}

// Store es un almacén en memoria para la PoC — se reemplaza por persistencia
// en SQLite en la Fase 4 sin cambiar la forma de uso de Begin/Complete/Fail.
type Store struct {
	mu      sync.Mutex
	entries map[string]*entry
	ttl     time.Duration
}

func NewStore(ttl time.Duration) *Store {
	return &Store{entries: make(map[string]*entry), ttl: ttl}
}

// Begin registra el inicio de un intento de ejecución para (integrationID,
// correlationID). Devuelve:
//   - inProgress=true si ya existe una solicitud en vuelo con esa clave (el
//     llamador debe responder 409 DUPLICATE_REQUEST sin ejecutar nada).
//   - cached≠nil si esa clave ya se completó con éxito dentro de la ventana
//     de deduplicación (el llamador debe reutilizar ese resultado).
//   - en cualquier otro caso, marca la clave como "en curso" y el llamador
//     debe proceder a ejecutar la integración y luego llamar a Complete o Fail.
func (s *Store) Begin(integrationID, correlationID string) (cached *core.SendResult, inProgress bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := integrationID + "|" + correlationID
	if e, ok := s.entries[key]; ok {
		if e.inProgress {
			return nil, true
		}
		if time.Now().Before(e.expiresAt) {
			result := e.result
			return &result, false
		}
		// Entrada vencida: se trata como una solicitud nueva.
	}

	s.entries[key] = &entry{inProgress: true}
	return nil, false
}

// Complete marca la solicitud como completada con éxito y cachea su
// resultado por la ventana de deduplicación configurada.
func (s *Store) Complete(integrationID, correlationID string, result core.SendResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := integrationID + "|" + correlationID
	s.entries[key] = &entry{result: result, expiresAt: time.Now().Add(s.ttl)}
}

// Fail libera la clave tras una ejecución fallida, permitiendo que un
// reintento con el mismo correlation_id vuelva a ejecutar la integración.
func (s *Store) Fail(integrationID, correlationID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, integrationID+"|"+correlationID)
}
