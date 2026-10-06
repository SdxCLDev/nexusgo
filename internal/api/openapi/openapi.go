// Package openapi embebe la especificación OpenAPI 3.0 del contrato público
// de Nexus — ver docs/03-contrato-api-rest.md. Sirve de documentación
// interactiva (Swagger UI, ver internal/api/handlers/docs.go) para explorar
// y probar la API desde un navegador sin acceso al código fuente — por
// ejemplo, operando el servicio desde un Windows Server.
package openapi

import _ "embed"

//go:embed openapi.json
var Spec []byte
