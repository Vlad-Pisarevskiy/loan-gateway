package main

import (
	"context"
	"log/slog"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/config"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/dispatch"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/store/postgres"
)

// runWorker обрабатывает очередь до отмены ctx.
func runWorker(ctx context.Context, cfg config.Gateway, log *slog.Logger) error {
	reg, _, err := buildBanks(cfg)
	if err != nil {
		return err
	}
	pool, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	w := dispatch.New(postgres.NewQueue(pool), reg, dispatch.Config{
		PollInterval:    cfg.PollInterval,
		BatchSize:       cfg.BatchSize,
		Concurrency:     cfg.Concurrency,
		Lease:           cfg.Lease,
		MaxAttempts:     cfg.MaxAttempts,
		BaseDelay:       cfg.BaseDelay,
		MaxDelay:        cfg.MaxDelay,
		CallbackTimeout: cfg.CallbackTimeout,
		SweepInterval:   cfg.SweepInterval,
		ShutdownTimeout: cfg.ShutdownTimeout,
		BankTimeouts:    bankTimeouts(cfg),
	}, log)
	return w.Run(ctx)
}
