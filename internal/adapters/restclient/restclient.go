// Package restclient es un cliente HTTP/REST genérico y reutilizable por las
// integraciones de Nexus (ver docs/02-arquitectura.md §2.5). Concentra la
// lógica de bajo nivel —timeouts, reintentos con backoff ante fallas
// transitorias, inyección de headers, (de)serialización JSON y logging con
// redacción de datos sensibles— para que cada integración (AMD, SAP, ...) no
// la reimplemente.
package restclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Client es un cliente REST configurado contra un baseURL. Es seguro para uso
// concurrente (http.Client lo es).
type Client struct {
	baseURL    string
	httpClient *http.Client
	maxRetries int
	baseDelay  time.Duration
	logger     *slog.Logger
	// redactHeaders son nombres de header (en minúsculas) cuyo valor nunca
	// debe aparecer en logs — ver docs/06-autenticacion-seguridad.md §6.6.
	redactHeaders map[string]struct{}
}

type Option func(*Client)

// WithTimeout fija el timeout total de cada request (incluye conexión, envío y
// lectura de la respuesta).
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.httpClient.Timeout = d
		}
	}
}

// WithRetries configura cuántos reintentos adicionales se hacen ante fallas
// transitorias (error de red, 5xx, 429) y el backoff base entre intentos.
func WithRetries(maxRetries int, baseDelay time.Duration) Option {
	return func(c *Client) {
		if maxRetries >= 0 {
			c.maxRetries = maxRetries
		}
		if baseDelay > 0 {
			c.baseDelay = baseDelay
		}
	}
}

// WithLogger adjunta un logger estructurado para el logging de requests.
func WithLogger(logger *slog.Logger) Option {
	return func(c *Client) {
		if logger != nil {
			c.logger = logger
		}
	}
}

// WithRedactedHeaders marca headers cuyo valor no debe loguearse (además de
// los redactados por defecto: Authorization y token).
func WithRedactedHeaders(names ...string) Option {
	return func(c *Client) {
		for _, n := range names {
			c.redactHeaders[strings.ToLower(n)] = struct{}{}
		}
	}
}

func New(baseURL string, opts ...Option) *Client {
	// Transport con pool de conexiones holgado: cuando varias goroutines
	// llaman al mismo host en paralelo (ej. la descarga concurrente de detalle
	// de minutas), reutilizan conexiones en vez de rehacer el handshake TLS en
	// cada llamada. El default de Go mantiene solo 2 conexiones ociosas por
	// host, lo que serializaría de hecho la reutilización bajo concurrencia.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 100
	transport.MaxIdleConnsPerHost = 100

	c := &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: 30 * time.Second, Transport: transport},
		maxRetries: 2,
		baseDelay:  200 * time.Millisecond,
		logger:     slog.Default(),
		redactHeaders: map[string]struct{}{
			"authorization": {},
			"token":         {},
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Request describe una solicitud. Body, si no es nil, se serializa como JSON y
// se envía con Content-Type: application/json.
type Request struct {
	Method  string
	Path    string
	Headers map[string]string
	Body    any
}

// Response es la respuesta cruda. StatusCode es el código HTTP; Body es el
// cuerpo completo ya leído. La decodificación la hace el llamador (DecodeJSON).
type Response struct {
	StatusCode int
	Body       []byte
}

// DecodeJSON deserializa el cuerpo de la respuesta en v.
func (r *Response) DecodeJSON(v any) error {
	if err := json.Unmarshal(r.Body, v); err != nil {
		return fmt.Errorf("restclient: respuesta JSON inválida (status %d): %w", r.StatusCode, err)
	}
	return nil
}

// Do ejecuta la solicitud, reintentando ante fallas transitorias. Devuelve la
// respuesta incluso con códigos 4xx/5xx (es el llamador quien interpreta el
// StatusCode); solo devuelve error cuando no hubo respuesta tras agotar los
// reintentos, o cuando falló la serialización del cuerpo o el contexto expiró.
func (c *Client) Do(ctx context.Context, req Request) (*Response, error) {
	var bodyBytes []byte
	if req.Body != nil {
		b, err := json.Marshal(req.Body)
		if err != nil {
			return nil, fmt.Errorf("restclient: no se pudo serializar el cuerpo: %w", err)
		}
		bodyBytes = b
	}

	url := c.baseURL + req.Path

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			if err := c.waitBackoff(ctx, attempt); err != nil {
				return nil, err
			}
		}

		resp, err := c.doOnce(ctx, req.Method, url, bodyBytes, req.Headers)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			c.logger.Warn("restclient: error transitorio, se reintentará",
				"method", req.Method, "url", url, "attempt", attempt+1, "error", err)
			continue
		}

		if isTransientStatus(resp.StatusCode) && attempt < c.maxRetries {
			lastErr = fmt.Errorf("restclient: status transitorio %d", resp.StatusCode)
			c.logger.Warn("restclient: status transitorio, se reintentará",
				"method", req.Method, "url", url, "attempt", attempt+1, "status", resp.StatusCode)
			continue
		}

		c.logger.Debug("restclient: respuesta",
			"method", req.Method, "url", url, "status", resp.StatusCode,
			"headers", c.redactedHeaders(req.Headers))
		return resp, nil
	}

	return nil, fmt.Errorf("restclient: se agotaron los reintentos para %s %s: %w", req.Method, url, lastErr)
}

func (c *Client) doOnce(ctx context.Context, method, url string, body []byte, headers map[string]string) (*Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("restclient: no se pudo construir la solicitud: %w", err)
	}
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("restclient: no se pudo leer la respuesta: %w", err)
	}

	return &Response{StatusCode: httpResp.StatusCode, Body: respBody}, nil
}

// waitBackoff espera un backoff exponencial simple (baseDelay * 2^(attempt-1)),
// respetando la cancelación del contexto.
func (c *Client) waitBackoff(ctx context.Context, attempt int) error {
	delay := c.baseDelay << (attempt - 1)
	select {
	case <-time.After(delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func isTransientStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
}

// redactedHeaders devuelve una copia de los headers con los valores sensibles
// reemplazados por "***", para logging seguro.
func (c *Client) redactedHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		if _, redacted := c.redactHeaders[strings.ToLower(k)]; redacted {
			out[k] = "***"
		} else {
			out[k] = v
		}
	}
	return out
}
