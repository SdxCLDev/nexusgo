// Package config carga la configuración de Nexus desde variables de entorno.
package config

import (
	"fmt"
	"os"
	"strconv"
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

	return &Config{
		Env:       env,
		HTTPAddr:  getEnv("NEXUS_HTTP_ADDR", ":8080"),
		LogLevel:  getEnv("NEXUS_LOG_LEVEL", "info"),
		DBPath:    getEnv("NEXUS_DB_PATH", "nexus.db"),
		JWTSecret: jwtSecret,
		TokenTTL:  time.Duration(tokenTTLSeconds) * time.Second,
		SGPAPIKey: sgpAPIKey,
	}, nil
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
