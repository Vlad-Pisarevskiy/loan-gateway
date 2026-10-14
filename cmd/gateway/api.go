package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/config"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/httpapi"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/store/postgres"
)

// runAPI обслуживает HTTP API до отмены ctx, затем корректно останавливает сервер.
func runAPI(ctx context.Context, cfg config.Gateway, log *slog.Logger) error {
	reg, hooks, err := buildBanks(cfg)
	if err != nil {
		return err
	}
	pool, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	handler := httpapi.NewHandler(httpapi.Deps{
		Applications:   application.NewService(postgres.NewApplications(pool), reg.Codes(), time.Now),
		Callbacks:      postgres.NewQueue(pool),
		DB:             pool,
		WebhookBanks:   hooks,
		WebhookMaxSkew: cfg.WebhookMaxSkew,
		Log:            log,
	})
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.Info("api started", "addr", cfg.HTTPAddr, "banks", reg.Codes())

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	log.Info("api stopping")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
