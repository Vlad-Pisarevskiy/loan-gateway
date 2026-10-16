package main

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/config"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/dispatch"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/store/postgres"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"
)

// runWorker обрабатывает очередь до отмены ctx; сервер метрик и трейсинг останавливаются
// вместе с воркером в пределах SHUTDOWN_TIMEOUT.
func runWorker(ctx context.Context, cfg config.Gateway, log *slog.Logger) error {
	reg, _, err := buildBanks(cfg)
	if err != nil {
		return err
	}
	shutdownTracing, err := telemetry.SetupTracing(ctx, os.Getenv, "gateway-worker", log)
	if err != nil {
		return err
	}
	pool, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	queue := postgres.NewQueue(pool)
	metricsReg := telemetry.NewRegistry()
	metrics := telemetry.NewMetrics(metricsReg)
	metrics.RegisterQueueStats(metricsReg, queue.Stats, queueStatsTimeout, reg.Codes())
	w := dispatch.New(queue, reg, dispatch.Config{
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
		Metrics:         metrics,
		Tracer:          telemetry.Tracer(),
	}, log)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	metricsErr, stopMetrics := startMetrics(cfg.MetricsAddr, metricsReg)
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	var exitErr error
	select {
	case exitErr = <-metricsErr: // без метрик воркер не работает: занятый порт — ошибка конфигурации
		cancel()
		<-done
	case exitErr = <-done:
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancelShutdown()
	return errors.Join(exitErr, stopMetrics(shutdownCtx), shutdownTracing(shutdownCtx))
}
