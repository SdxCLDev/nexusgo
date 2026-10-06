package handlers

import (
	"net/http"

	"nexusgo/internal/api/openapi"
)

// OpenAPISpec sirve el spec OpenAPI embebido — ver internal/api/openapi.
func OpenAPISpec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(openapi.Spec)
}

// swaggerUIPage carga Swagger UI desde un CDN (no se embebe en el binario:
// son ~2MB de JS/CSS de un proyecto de terceros, mientras que el spec que
// describe —openapi.json— sí viaja embebido). Requiere que el navegador que
// abre /docs tenga salida a internet; el propio Nexus no la necesita.
const swaggerUIPage = `<!doctype html>
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
        url: "/openapi.json",
        dom_id: "#swagger-ui",
      });
    };
  </script>
</body>
</html>
`

// SwaggerUI implementa GET /docs — ver docs/10-plan-de-trabajo-poc.md.
func SwaggerUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(swaggerUIPage))
}
