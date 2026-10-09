// Package config carga la configuración de Nexus desde variables de entorno.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	devJWTSecret = "dev-insecure-secret-do-not-use-in-prod"
	devSGPAPIKey = "sgp-dev-local-key"
)

type Config struct {
	Env      string // dev | test | prod
	HTTPAddr string
	LogLevel string
	DBPath   string

	// JWTSecret firma los JWT emitidos por /auth/token — ver
	// docs/06-autenticacion-seguridad.md §6.1. Simplificación de PoC: HS256
	// con clave simétrica en vez de RS256 (ver internal/auth/jwt.go).
	JWTSecret string
	TokenTTL  time.Duration

	// SGPAPIKey es la API Key del cliente "sgp" sembrado en memoria durante
	// la PoC (ver docs/10-plan-de-trabajo-poc.md Fase 2). Se reemplaza por
	// un alta administrativa persistida en la Fase 4.
	SGPAPIKey string

	// IdempotencyTTL es la ventana de deduplicación por correlation_id — ver
	// docs/03-contrato-api-rest.md §3.9.
	IdempotencyTTL time.Duration

	// JobConcurrency limita cuántos jobs asíncronos se ejecutan en paralelo
	// — ver docs/05-patron-asincrono.md §5.8 y internal/core/jobmanager.
	JobConcurrency int

	// PublicBasePath es el prefijo bajo el que un reverse proxy expone a
	// Nexus (ej. "/nexus" si se publica en https://host/nexus/). Nexus
	// siempre escucha en la raíz internamente; este valor solo se usa para
	// construir URLs absolutas en las respuestas (status_url, result_url,
	// el spec OpenAPI y la página de Swagger UI) de forma que funcionen a
	// través del proxy. Vacío por defecto (sin proxy, o proxy en la raíz).
	PublicBasePath string

	// CredentialKey es el secreto con que se cifran en reposo las credenciales
	// externas (ver internal/credentials y docs/06-autenticacion-seguridad.md
	// §6.4). Vacío solo se acepta en 'dev' (se guardan sin cifrar).
	CredentialKey string

	// AMD es la configuración de conexión hacia el sistema externo AMD — ver
	// docs/05-patron-asincrono.md. Solo BaseURL cambia por ambiente; User y
	// Password se usan únicamente para sembrar external_credentials al arrancar.
	AMD AMDConfig
}

// AMDConfig agrupa la configuración de la integración con AMD.
type AMDConfig struct {
	BaseURL           string
	User              string
	Password          string
	Timeout           time.Duration
	DetailConcurrency int
}

func Load() (*Config, error) {
	env := getEnv("NEXUS_ENV", "dev")

	jwtSecret, err := requireInDevOrEnv(env, "NEXUS_JWT_SECRET", devJWTSecret)
	if err != nil {
		return nil, err
	}

	sgpAPIKey, err := requireInDevOrEnv(env, "NEXUS_SGP_API_KEY", devSGPAPIKey)
	if err != nil {
		return nil, err
	}

	tokenTTLSeconds, err := getEnvInt("NEXUS_TOKEN_TTL_SECONDS", 900)
	if err != nil {
		return nil, err
	}

	idempotencyTTLHours, err := getEnvInt("NEXUS_IDEMPOTENCY_TTL_HOURS", 24)
	if err != nil {
		return nil, err
	}

	jobConcurrency, err := getEnvInt("NEXUS_JOB_CONCURRENCY", 5)
	if err != nil {
		return nil, err
	}

	amdTimeoutSeconds, err := getEnvInt("NEXUS_AMD_TIMEOUT_SECONDS", 30)
	if err != nil {
		return nil, err
	}

	amdDetailConcurrency, err := getEnvInt("NEXUS_AMD_DETAIL_CONCURRENCY", 10)
	if err != nil {
		return nil, err
	}

	return &Config{
		Env:            env,
		HTTPAddr:       getEnv("NEXUS_HTTP_ADDR", ":8080"),
		LogLevel:       getEnv("NEXUS_LOG_LEVEL", "info"),
		DBPath:         getEnv("NEXUS_DB_PATH", "nexus.db"),
		JWTSecret:      jwtSecret,
		TokenTTL:       time.Duration(tokenTTLSeconds) * time.Second,
		SGPAPIKey:      sgpAPIKey,
		IdempotencyTTL: time.Duration(idempotencyTTLHours) * time.Hour,
		JobConcurrency: jobConcurrency,
		PublicBasePath: normalizeBasePath(getEnv("NEXUS_PUBLIC_BASE_PATH", "")),
		CredentialKey:  getEnv("NEXUS_CRED_KEY", ""),
		AMD: AMDConfig{
			BaseURL:           getEnv("NEXUS_AMD_BASE_URL", "https://amddev.sodexhochile.cl"),
			User:              getEnv("NEXUS_AMD_USER", ""),
			Password:          getEnv("NEXUS_AMD_PASSWORD", ""),
			Timeout:           time.Duration(amdTimeoutSeconds) * time.Second,
			DetailConcurrency: amdDetailConcurrency,
		},
	}, nil
}

// normalizeBasePath acepta "", "/nexus", "nexus" o "/nexus/" y siempre
// devuelve "" o una ruta con barra inicial y sin barra final (ej. "/nexus").
func normalizeBasePath(raw string) string {
	trimmed := strings.Trim(raw, "/")
	if trimmed == "" {
		return ""
	}
	return "/" + trimmed
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("config: %s inválido: %w", key, err)
	}
	return n, nil
}

// requireInDevOrEnv usa un valor por defecto solo en ambiente "dev"; en
// cualquier otro ambiente exige que la variable de entorno esté definida,
// para evitar arrancar producción con secretos de desarrollo.
func requireInDevOrEnv(env, key, devFallback string) (string, error) {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v, nil
	}
	if env == "dev" {
		return devFallback, nil
	}
	return "", fmt.Errorf("config: %s es obligatorio fuera del ambiente 'dev'", key)
}
