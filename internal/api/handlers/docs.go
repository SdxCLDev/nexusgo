package handlers

import (
	"fmt"
	"net/http"

	"nexusgo/internal/api/openapi"
)

// OpenAPISpec sirve el spec OpenAPI con "servers" ajustado a basePath (ver
// internal/api/openapi.BuildSpec) — se construye una sola vez al armar el
// router, no en cada request.
func OpenAPISpec(basePath string) http.HandlerFunc {
	specBytes, err := openapi.BuildSpec(basePath)
	if err != nil {
		// El spec embebido es un asset propio fijado en tiempo de build: si
		// esto falla es un error de programación (JSON inválido en
		// internal/api/openapi/openapi.json), no una condición de runtime.
		panic(fmt.Sprintf("handlers: no se pudo construir el spec OpenAPI: %v", err))
	}

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(specBytes)
	}
}

// swaggerUIPageTemplate carga Swagger UI desde un CDN (no se embebe en el
// binario: son ~2MB de JS/CSS de un proyecto de terceros, mientras que el
// spec que describe —openapi.json— sí viaja embebido). Requiere que el
// navegador que abre /docs tenga salida a internet; el propio Nexus no la
// necesita. %s se reemplaza por basePath + "/openapi.json".
const swaggerUIPageTemplate = `<!doctype html>
<html>
<head>
  <meta charset="utf-8" />
  <title>Nexus API</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css" />
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    window.onload = () => {
      window.ui = SwaggerUIBundle({
        url: %q,
        dom_id: "#swagger-ui",
      });
    };
  </script>
</body>
</html>
`

// SwaggerUI implementa GET /docs — ver docs/10-plan-de-trabajo-poc.md.
func SwaggerUI(basePath string) http.HandlerFunc {
	page := fmt.Sprintf(swaggerUIPageTemplate, basePath+"/openapi.json")

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}
}
