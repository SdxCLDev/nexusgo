package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"nexusgo/internal/api"
	"nexusgo/internal/config"
	"nexusgo/internal/core"
	"nexusgo/internal/integrations/mock"
	"nexusgo/internal/logging"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error cargando configuración:", err)
		os.Exit(1)
	}

	logger := logging.New(cfg)

	reg := buildRegistry()
	router := api.NewRouter(logger, reg)

	srv := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: router,
	}

	go func() {
		logger.Info("iniciando servidor", "addr", cfg.HTTPAddr, "env", cfg.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("error del servidor", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	logger.Info("apagando servidor")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("error durante el apagado", "error", err)
	}
}

// buildRegistry da de alta las integraciones disponibles — ver
// docs/02-arquitectura.md §2.4. Las integraciones reales (AMD, SAP) se
// agregan aquí a medida que se implementan (Fases 6-8 del plan de trabajo).
func buildRegistry() *core.Registry {
	reg := core.NewRegistry()

	reg.Register(mock.NewEchoIntegration())

	return reg
}
