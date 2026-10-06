// Package dto contiene las estructuras de request/response del contrato
// público de la API — ver docs/03-contrato-api-rest.md.
package dto

type SendSuccessResponse struct {
	CorrelationID string `json:"correlation_id"`
	IntegrationID string `json:"integration_id"`
	Status        string `json:"status"`
	Code          string `json:"code"`
	Message       string `json:"message,omitempty"`
	Data          any    `json:"data,omitempty"`
	Timestamp     string `json:"timestamp"`
}

type ErrorResponse struct {
	CorrelationID string `json:"correlation_id,omitempty"`
	IntegrationID string `json:"integration_id,omitempty"`
	Status        string `json:"status"`
	Code          string `json:"code"`
	Message       string `json:"message"`
	Details       any    `json:"details,omitempty"`
	Timestamp     string `json:"timestamp"`
}

type IntegrationSummary struct {
	IntegrationID string `json:"integration_id"`
	Name          string `json:"name"`
	Direction     string `json:"direction"`
	Mode          string `json:"mode"`
	Version       string `json:"version"`
	Status        string `json:"status"`
}

type CatalogResponse struct {
	Integrations []IntegrationSummary `json:"integrations"`
}

type TokenRequest struct {
	ClientID string `json:"client_id"`
	APIKey   string `json:"api_key"`
}

type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// AsyncAcceptedResponse es la respuesta de aceptación de un job — ver
// docs/03-contrato-api-rest.md §3.6.
type AsyncAcceptedResponse struct {
	CorrelationID string `json:"correlation_id"`
	IntegrationID string `json:"integration_id"`
	JobID         string `json:"job_id"`
	Status        string `json:"status"`
	StatusURL     string `json:"status_url"`
	Timestamp     string `json:"timestamp"`
}

type JobProgress struct {
	Total     *int `json:"total,omitempty"`
	Processed int  `json:"processed"`
	Failed    int  `json:"failed"`
}

// JobStatusResponse es la respuesta de GET /jobs/{job_id} — ver
// docs/03-contrato-api-rest.md §3.3 y docs/05-patron-asincrono.md §5.6.
type JobStatusResponse struct {
	JobID         string      `json:"job_id"`
	IntegrationID string      `json:"integration_id"`
	CorrelationID string      `json:"correlation_id"`
	Status        string      `json:"status"`
	Progress      JobProgress `json:"progress"`
	CreatedAt     string      `json:"created_at"`
	UpdatedAt     string      `json:"updated_at"`
	FinishedAt    *string     `json:"finished_at"`
	ResultURL     string      `json:"result_url,omitempty"`
}

type JobItemResponse struct {
	ExternalID string `json:"external_id"`
	Status     string `json:"status"`
	Data       any    `json:"data,omitempty"`
	Error      string `json:"error,omitempty"`
}

type JobResultSummary struct {
	Total   int `json:"total"`
	Success int `json:"success"`
	Failed  int `json:"failed"`
}

// JobResultResponse es la respuesta de GET /jobs/{job_id}/result — ver
// docs/05-patron-asincrono.md §5.7.
type JobResultResponse struct {
	JobID         string            `json:"job_id"`
	IntegrationID string            `json:"integration_id"`
	Summary       JobResultSummary  `json:"summary"`
	Items         []JobItemResponse `json:"items"`
}
