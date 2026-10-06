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
