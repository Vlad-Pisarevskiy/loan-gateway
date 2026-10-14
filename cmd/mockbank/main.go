// Command mockbank — заглушка банка-партнёра; диалект выбирается переменной MOCKBANK_DIALECT.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/config"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/mockbank"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "mockbank:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadMockbank(os.Getenv)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	handler, err := newHandler(ctx, cfg, log)
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: cfg.Addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.Info("mockbank started", "dialect", cfg.Dialect, "addr", cfg.Addr)

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// newHandler выбирает диалект заглушки. ctx и log понадобятся асинхронному диалекту west.
func newHandler(_ context.Context, cfg config.Mockbank, _ *slog.Logger) (http.Handler, error) {
	opts := mockbank.Options{Latency: cfg.Latency, FailFirst: cfg.FailFirst}
	switch cfg.Dialect {
	case "north":
		return mockbank.NewNorth(opts), nil
	default:
		return nil, fmt.Errorf("dialect %q is not implemented", cfg.Dialect)
	}
}
