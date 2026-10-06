// Package core contiene el contrato interno de Nexus: la interfaz que toda
// integración implementa, el envelope genérico, el registro de integraciones
// y los errores estándar del dominio.
package core

type Direction string

const (
	DirectionOutbound      Direction = "OUTBOUND"
	DirectionInbound       Direction = "INBOUND"
	DirectionBidirectional Direction = "BIDIRECTIONAL"
)

type Mode string

const (
	ModeSync  Mode = "SYNC"
	ModeAsync Mode = "ASYNC"
)

// Metadata describe una integración registrada: la información que se
// expone en el catálogo público (GET /integrations).
type Metadata struct {
	ID        string
	Name      string
	Direction Direction
	Mode      Mode
	Version   string
}
