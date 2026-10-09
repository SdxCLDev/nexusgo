// Package credentials modela las credenciales de salida de Nexus hacia los
// sistemas externos (AMD, SAP, PEL, ...) — ver docs/06-autenticacion-seguridad.md
// §6.3/§6.4 y docs/08-modelo-datos.md §8.5.
//
// El consumidor de Nexus (SGP) nunca ve estas credenciales: viven cifradas en
// reposo y solo las usa internamente el paquete de integración correspondiente.
// La persistencia concreta la implementa internal/storage/sqlite.CredentialStore.
package credentials

import "context"

// Tipos de credencial soportados — ver docs/08-modelo-datos.md §8.5.
const (
	TypeBasic  = "BASIC"  // usuario/clave (ej. AMD)
	TypeOAuth2 = "OAUTH2" // client_id/client_secret
	TypeAPIKey = "API_KEY"
)

// Credential son las credenciales ya descifradas de Nexus hacia un sistema
// externo, en un ambiente dado. Payload es un mapa clave/valor cuyo contenido
// depende de Type (ej. para BASIC: {"user":..., "password":...}).
type Credential struct {
	ExternalSystem string
	Type           string
	Environment    string
	Payload        map[string]string
}

// Get devuelve un valor del payload; útil para evitar accesos a mapa dispersos.
func (c Credential) Get(key string) string { return c.Payload[key] }

// Store persiste y recupera credenciales externas. La implementación cifra el
// payload en reposo (ver Cipher); Get lo devuelve ya descifrado.
type Store interface {
	Get(ctx context.Context, externalSystem, environment string) (Credential, bool, error)
	Upsert(ctx context.Context, c Credential) error
}
