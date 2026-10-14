// Command gateway — шлюз заявок на кредит. Подкоманды: api, worker, migrate.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/config"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/store/postgres"
)

const usage = "usage: gateway api|worker|migrate"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gateway:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 1 {
		return errors.New(usage)
	}
	cfg, err := config.LoadGateway(os.Getenv, bankCodes())
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch args[0] {
	case "migrate":
		if err := postgres.Migrate(ctx, cfg.DatabaseURL); err != nil {
			return err
		}
		log.Info("migrations applied")
		return nil
	case "api":
		return runAPI(ctx, cfg, log)
	case "worker":
		return runWorker(ctx, cfg, log)
	default:
		return fmt.Errorf("unknown command %q; %s", args[0], usage)
	}
}
