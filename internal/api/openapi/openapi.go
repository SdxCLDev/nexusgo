// Package openapi embebe la especificación OpenAPI 3.0 del contrato público
// de Nexus — ver docs/03-contrato-api-rest.md. Sirve de documentación
// interactiva (Swagger UI, ver internal/api/handlers/docs.go) para explorar
// y probar la API desde un navegador sin acceso al código fuente — por
// ejemplo, operando el servicio desde un Windows Server o detrás de un
// reverse proxy en un sub-path (ver BuildSpec).
package openapi

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed openapi.json
var rawSpec []byte

// BuildSpec devuelve el spec con "servers" seteado según basePath (ej.
// "/nexus" si Nexus se publica detrás de un reverse proxy en ese sub-path).
// Todas las rutas del spec son absolutas desde la raíz ("/api/v1/..."); sin
// "servers", Swagger UI resolvería "Try it out" contra el dominio raíz,
// ignorando el sub-path del proxy — ver docs/10-plan-de-trabajo-poc.md.
func BuildSpec(basePath string) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(rawSpec, &doc); err != nil {
		return nil, fmt.Errorf("openapi: spec embebido inválido: %w", err)
	}

	serverURL := basePath
	if serverURL == "" {
		serverURL = "/"
	}
	doc["servers"] = []map[string]string{{"url": serverURL}}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("openapi: no se pudo serializar el spec: %w", err)
	}
	return buf.Bytes(), nil
}
