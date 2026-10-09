// Package amd implementa las integraciones de Nexus con la plataforma AMD —
// ver docs/05-patron-asincrono.md (caso de referencia: descarga de minutas) y
// docs/09-guia-nueva-integracion.md. Toda la particularidad de AMD (su esquema
// de autenticación, su endpoint genérico de stored procedures y el formato de
// sus datos) queda encapsulada aquí; el núcleo de Nexus no la conoce.
package amd

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"nexusgo/internal/adapters/restclient"
	"nexusgo/internal/credentials"
)

const (
	// externalSystem es la clave bajo la que se guardan las credenciales de
	// AMD en external_credentials — ver docs/08-modelo-datos.md §8.5.
	externalSystem = "AMD"

	pathLogin  = "/net/Authenticate/Login"
	pathCallSP = "/net/CallSP/getSP"

	// AMD transporta el token en un header llamado literalmente "token", con
	// el JWT crudo (sin prefijo "Bearer"). Es específico de AMD, no del
	// esquema de autenticación de entrada de Nexus.
	tokenHeader = "token"

	// Stored procedures de AMD usados por la descarga de minutas.
	spGetHeaders = "amd_get_minutasintegra"
	spGetDetail  = "amd_getMinutaIntegracion"

	// cecoAll (-1) le pide a AMD todas las minutas pendientes sin filtrar por
	// centro de costo: el proceso descarga todo lo pendiente (no hay filtros).
	cecoAll = "-1"

	// tokenSkew renueva el token un poco antes de su expiración real para
	// evitar usarlo justo cuando está por vencer.
	tokenSkew = 30 * time.Second
)

// Client habla con AMD: autentica (canjea usuario/clave por un api_token),
// cachea ese token en memoria con su expiración, y ejecuta los stored
// procedures expuestos por el endpoint genérico /net/CallSP/getSP. Es seguro
// para uso concurrente. Ver docs/06-autenticacion-seguridad.md §6.3.
type Client struct {
	rest        *restclient.Client
	creds       credentials.Store
	environment string
	logger      *slog.Logger

	mu       sync.Mutex
	token    string
	tokenExp time.Time
	userID   string
}

func NewClient(rest *restclient.Client, creds credentials.Store, environment string, logger *slog.Logger) *Client {
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{rest: rest, creds: creds, environment: environment, logger: logger}
}

// --- Autenticación (paso 1 del flujo, docs/05-patron-asincrono.md §5.3) ---

type loginResult struct {
	ID       int    `json:"id"`
	APIToken string `json:"api_token"`
}

type loginResponse struct {
	Status     int         `json:"status"`
	Result     loginResult `json:"result"`
	Expiration string      `json:"expiration"`
}

// login autentica contra AMD y cachea el token resultante. Debe llamarse con
// c.mu tomado.
func (c *Client) login(ctx context.Context) error {
	cred, found, err := c.creds.Get(ctx, externalSystem, c.environment)
	if err != nil {
		return fmt.Errorf("no se pudieron leer las credenciales de AMD: %w", err)
	}
	if !found {
		return fmt.Errorf("no hay credenciales configuradas para AMD en el ambiente %q", c.environment)
	}

	resp, err := c.rest.Do(ctx, restclient.Request{
		Method: http.MethodPost,
		Path:   pathLogin,
		Body: map[string]string{
			"user":     cred.Get("user"),
			"password": cred.Get("password"),
		},
	})
	if err != nil {
		return fmt.Errorf("error de comunicación autenticando en AMD: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("AMD rechazó la autenticación (HTTP %d)", resp.StatusCode)
	}

	var lr loginResponse
	if err := resp.DecodeJSON(&lr); err != nil {
		return fmt.Errorf("respuesta de autenticación de AMD ilegible: %w", err)
	}
	if lr.Result.APIToken == "" {
		return fmt.Errorf("AMD no devolvió api_token (status=%d)", lr.Status)
	}

	exp, err := time.Parse(time.RFC3339, lr.Expiration)
	if err != nil {
		// Sin expiración válida, cachear por un lapso corto y conservador.
		exp = time.Now().Add(5 * time.Minute)
	}

	c.token = lr.Result.APIToken
	c.tokenExp = exp
	c.userID = strconv.Itoa(lr.Result.ID)
	c.logger.Info("autenticado en AMD", "user_id", c.userID, "token_expira", exp.Format(time.RFC3339))
	return nil
}

// ensureToken devuelve un token vigente (reautenticando si falta o expiró) y
// el id de usuario que AMD asoció a esa sesión.
func (c *Client) ensureToken(ctx context.Context) (token, userID string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenExp.Add(-tokenSkew)) {
		return c.token, c.userID, nil
	}
	if err := c.login(ctx); err != nil {
		return "", "", err
	}
	return c.token, c.userID, nil
}

// reauth fuerza una reautenticación tras un 401, evitando una estampida: si
// otro goroutine ya renovó el token (cambió respecto a staleToken), lo reusa.
func (c *Client) reauth(ctx context.Context, staleToken string) (token, userID string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && c.token != staleToken {
		return c.token, c.userID, nil
	}
	if err := c.login(ctx); err != nil {
		return "", "", err
	}
	return c.token, c.userID, nil
}

// --- Invocación de stored procedures (/net/CallSP/getSP) ---

type spParameter struct {
	Text  string `json:"text"`
	Value string `json:"value"`
}

type spRequest struct {
	Name       string        `json:"name"`
	Parameters []spParameter `json:"parameters"`
}

type spResponse struct {
	Result json.RawMessage `json:"result"`
	Status string          `json:"status"`
}

// callSP ejecuta un stored procedure de AMD. Ante un 401 reautentica una vez y
// reintenta, según docs/06-autenticacion-seguridad.md §6.3.
func (c *Client) callSP(ctx context.Context, name string, params []spParameter) (json.RawMessage, error) {
	token, _, err := c.ensureToken(ctx)
	if err != nil {
		return nil, err
	}

	result, status, err := c.doCallSP(ctx, token, name, params)
	if err != nil {
		return nil, err
	}

	if status == http.StatusUnauthorized {
		c.logger.Warn("AMD devolvió 401; reautenticando y reintentando", "sp", name)
		token, _, err = c.reauth(ctx, token)
		if err != nil {
			return nil, err
		}
		result, status, err = c.doCallSP(ctx, token, name, params)
		if err != nil {
			return nil, err
		}
	}

	if status != http.StatusOK {
		return nil, fmt.Errorf("AMD devolvió HTTP %d al ejecutar %q", status, name)
	}
	return result, nil
}

// doCallSP ejecuta una llamada única. Devuelve el "result" crudo solo cuando el
// status es 200; para otros códigos devuelve (nil, status, nil) para que callSP
// decida (reautenticar ante 401, error en el resto).
func (c *Client) doCallSP(ctx context.Context, token, name string, params []spParameter) (json.RawMessage, int, error) {
	resp, err := c.rest.Do(ctx, restclient.Request{
		Method:  http.MethodPost,
		Path:    pathCallSP,
		Headers: map[string]string{tokenHeader: token},
		Body:    spRequest{Name: name, Parameters: params},
	})
	if err != nil {
		return nil, 0, fmt.Errorf("error de comunicación con AMD ejecutando %q: %w", name, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, nil
	}
	var sp spResponse
	if err := resp.DecodeJSON(&sp); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("respuesta de AMD ilegible para %q: %w", name, err)
	}
	return sp.Result, resp.StatusCode, nil
}

// --- Operaciones de negocio ---

// GetMinutaHeaders obtiene los encabezados de todas las minutas pendientes
// (paso 2 del flujo). El parámetro "usuario" es el id del usuario autenticado.
func (c *Client) GetMinutaHeaders(ctx context.Context) ([]MinutaHeader, error) {
	_, userID, err := c.ensureToken(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := c.callSP(ctx, spGetHeaders, []spParameter{
		{Text: "usuario", Value: userID},
		{Text: "ceco", Value: cecoAll},
	})
	if err != nil {
		return nil, err
	}
	var headers []MinutaHeader
	if err := json.Unmarshal(raw, &headers); err != nil {
		return nil, fmt.Errorf("no se pudieron interpretar los encabezados de minuta de AMD: %w", err)
	}
	return headers, nil
}

// GetMinutaDetail obtiene el detalle de una minuta por su id (paso 3 del flujo).
func (c *Client) GetMinutaDetail(ctx context.Context, idMinuta int) ([]MinutaDetalle, error) {
	raw, err := c.callSP(ctx, spGetDetail, []spParameter{
		{Text: "id", Value: strconv.Itoa(idMinuta)},
	})
	if err != nil {
		return nil, err
	}
	var detalle []MinutaDetalle
	if err := json.Unmarshal(raw, &detalle); err != nil {
		return nil, fmt.Errorf("no se pudo interpretar el detalle de la minuta %d de AMD: %w", idMinuta, err)
	}
	return detalle, nil
}
