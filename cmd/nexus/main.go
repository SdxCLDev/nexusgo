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
	"nexusgo/internal/api/handlers"
	"nexusgo/internal/auth"
	"nexusgo/internal/config"
	"nexusgo/internal/core"
	"nexusgo/internal/core/idempotency"
	"nexusgo/internal/core/jobmanager"
	"nexusgo/internal/integrations/mock"
	"nexusgo/internal/jobs"
	"nexusgo/internal/logging"
	"nexusgo/internal/storage/sqlite"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error cargando configuración:", err)
		os.Exit(1)
	}

	logger := logging.New(cfg)
	ctx := context.Background()

	db, err := sqlite.Open(ctx, cfg.DBPath)
	if err != nil {
		logger.Error("no se pudo inicializar la base de datos", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	clientStore := sqlite.NewClientStore(db)
	if err := seedClients(ctx, clientStore, cfg); err != nil {
		logger.Error("no se pudo sembrar los clientes iniciales", "error", err)
		os.Exit(1)
	}

	inboxStore := sqlite.NewSimulatedInboxStore(db)
	reg := buildRegistry(inboxStore)
	catalogStore := sqlite.NewCatalogStore(db)
	if err := catalogStore.Sync(ctx, reg.List()); err != nil {
		logger.Error("no se pudo sincronizar el catálogo de integraciones", "error", err)
		os.Exit(1)
	}

	auditStore := sqlite.NewAuditStore(db)
	jobStore := sqlite.NewJobStore(db)
	jobManager := jobmanager.NewManager(jobStore, auditStore, cfg.JobConcurrency, logger)

	router := api.NewRouter(api.Deps{
		Logger:       logger,
		Registry:     reg,
		CatalogStore: catalogStore,
		ClientStore:  clientStore,
		JWTSecret:    []byte(cfg.JWTSecret),
		TokenTTL:     cfg.TokenTTL,
		AuditStore:   auditStore,
		Idempotency:  idempotency.NewStore(cfg.IdempotencyTTL),
		JobStore:     jobStore,
		JobManager:   jobManager,
		BasePath:     cfg.PublicBasePath,
		ReadyChecks: []handlers.ReadyCheck{
			func() error {
				pingCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				return db.PingContext(pingCtx)
			},
		},
	})

	srv := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: router,
	}

	go func() {
		logger.Info("iniciando servidor", "addr", cfg.HTTPAddr, "env", cfg.Env, "db_path", cfg.DBPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("error del servidor", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	logger.Info("apagando servidor")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("error durante el apagado", "error", err)
	}
}

// buildRegistry da de alta las integraciones disponibles — ver
// docs/02-arquitectura.md §2.4. Las integraciones reales (AMD, SAP) se
// agregan aquí a medida que se implementan (Fases 6-8 del plan de trabajo).
// mock-batch-pull y mock-batch-push comparten la misma lógica
// (internal/integrations/mock.BatchIntegration) pero demuestran las dos
// modalidades de entrega — ver docs/05-patron-asincrono.md §5.5.
func buildRegistry(inbox mock.SimulatedInbox) *core.Registry {
	reg := core.NewRegistry()

	reg.Register(mock.NewEchoIntegration())
	reg.Register(mock.NewBatchIntegration("mock-batch-pull", jobs.DeliveryPullAPI, inbox))
	reg.Register(mock.NewBatchIntegration("mock-batch-push", jobs.DeliveryPushDB, inbox))

	return reg
}

// seedClients da de alta los clientes autorizados de la PoC — ver
// docs/08-modelo-datos.md §8.1. El alta administrativa real de clientes
// queda fuera de alcance de esta fase (ver docs/10-plan-de-trabajo-poc.md
// Fase 10); por ahora Nexus siembra el cliente "sgp" en cada arranque.
func seedClients(ctx context.Context, store *sqlite.ClientStore, cfg *config.Config) error {
	return store.Upsert(ctx, auth.Client{
		ID:         "sgp",
		APIKeyHash: auth.HashAPIKey(cfg.SGPAPIKey),
		Scopes: []string{
			"integration:mock-echo:invoke",
			"integration:mock-batch-pull:invoke",
			"integration:mock-batch-push:invoke",
		},
		Status: auth.ClientStatusActive,
	})
}
