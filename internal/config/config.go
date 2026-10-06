// Package config carga la configuración de Nexus desde variables de entorno.
package config

import "os"

type Config struct {
	Env      string // dev | test | prod
	HTTPAddr string
	LogLevel string
	DBPath   string
}

func Load() (*Config, error) {
	return &Config{
		Env:      getEnv("NEXUS_ENV", "dev"),
		HTTPAddr: getEnv("NEXUS_HTTP_ADDR", ":8080"),
		LogLevel: getEnv("NEXUS_LOG_LEVEL", "info"),
		DBPath:   getEnv("NEXUS_DB_PATH", "nexus.db"),
	}, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
