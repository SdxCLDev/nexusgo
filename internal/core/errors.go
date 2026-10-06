package core

import (
	"fmt"
	"net/http"
)

// Códigos de error estándar del contrato — ver docs/03-contrato-api-rest.md §3.8.
const (
	CodeInvalidRequest         = "INVALID_REQUEST"
	CodeInvalidEnvelope        = "INVALID_ENVELOPE"
	CodeInvalidPayload         = "INVALID_PAYLOAD"
	CodeUnauthorized           = "UNAUTHORIZED"
	CodeForbidden              = "FORBIDDEN"
	CodeIntegrationNotFound    = "INTEGRATION_NOT_FOUND"
	CodeExternalSystemError    = "EXTERNAL_SYSTEM_ERROR"
	CodeBusinessRuleRejected   = "BUSINESS_RULE_REJECTED"
	CodeIntegrationUnavailable = "INTEGRATION_UNAVAILABLE"
	CodeInternalError          = "INTERNAL_ERROR"
)

// Error es el error de dominio que toda integración y el núcleo deben
// devolver para que la API lo traduzca al formato estándar de respuesta.
type Error struct {
	Code       string
	Message    string
	HTTPStatus int
	Details    any
	cause      error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.cause }

// WithDetails adjunta detalle adicional (ej. respuesta cruda del sistema externo).
func (e *Error) WithDetails(details any) *Error {
	e.Details = details
	return e
}

func newError(code string, httpStatus int, format string, args ...any) *Error {
	cause := fmt.Errorf(format, args...)
	return &Error{Code: code, HTTPStatus: httpStatus, Message: cause.Error(), cause: cause}
}

func NewInvalidRequestError(format string, args ...any) *Error {
	return newError(CodeInvalidRequest, http.StatusBadRequest, format, args...)
}

func NewEnvelopeError(format string, args ...any) *Error {
	return newError(CodeInvalidEnvelope, http.StatusBadRequest, format, args...)
}

func NewValidationError(format string, args ...any) *Error {
	return newError(CodeInvalidPayload, http.StatusBadRequest, format, args...)
}

func NewUnauthorizedError(format string, args ...any) *Error {
	return newError(CodeUnauthorized, http.StatusUnauthorized, format, args...)
}

func NewForbiddenError(format string, args ...any) *Error {
	return newError(CodeForbidden, http.StatusForbidden, format, args...)
}

func NewNotFoundError(format string, args ...any) *Error {
	return newError(CodeIntegrationNotFound, http.StatusNotFound, format, args...)
}

func NewExternalError(format string, args ...any) *Error {
	return newError(CodeExternalSystemError, http.StatusBadGateway, format, args...)
}

func NewBusinessRuleError(format string, args ...any) *Error {
	return newError(CodeBusinessRuleRejected, http.StatusUnprocessableEntity, format, args...)
}

func NewUnavailableError(format string, args ...any) *Error {
	return newError(CodeIntegrationUnavailable, http.StatusServiceUnavailable, format, args...)
}

func NewInternalError(format string, args ...any) *Error {
	return newError(CodeInternalError, http.StatusInternalServerError, format, args...)
}
