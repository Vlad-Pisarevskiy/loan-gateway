// Package httpapi — HTTP API шлюза: заявки, вебхуки банков, health-проверки.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
)

// ApplicationService — сценарии заявок; реализация — application.Service.
type ApplicationService interface {
	Create(ctx context.Context, key string, in application.Input) (application.View, error)
	Get(ctx context.Context, id uuid.UUID) (application.View, error)
}

// Pinger проверяет доступность БД; подходит *pgxpool.Pool.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Deps — зависимости API.
type Deps struct {
	Applications ApplicationService
	DB           Pinger
	Log          *slog.Logger
}

type api struct {
	Deps
}

// NewHandler собирает роутер API.
func NewHandler(d Deps) http.Handler {
	a := &api{Deps: d}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/applications", a.createApplication)
	mux.HandleFunc("GET /v1/applications/{id}", a.getApplication)
	mux.HandleFunc("GET /healthz", a.healthz)
	mux.HandleFunc("GET /readyz", a.readyz)
	return mux
}
