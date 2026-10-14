// Package httpapi — HTTP API шлюза: заявки, вебхуки банков, health-проверки.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
)

// ApplicationService — сценарии заявок; реализация — application.Service.
type ApplicationService interface {
	Create(ctx context.Context, key string, in application.Input) (application.View, error)
	Get(ctx context.Context, id uuid.UUID) (application.View, error)
}

// CallbackStore применяет решения из вебхуков; реализация — store/postgres.Queue.
type CallbackStore interface {
	ApplyCallback(ctx context.Context, bankCode, externalID string, d application.Decision) (application.CallbackResult, error)
}

// Pinger проверяет доступность БД; подходит *pgxpool.Pool.
type Pinger interface {
	Ping(ctx context.Context) error
}

// WebhookBank — асинхронный банк: разбор вебхука и секрет подписи.
type WebhookBank struct {
	Handler bank.CallbackHandler
	Secret  []byte
}

// Deps — зависимости API.
type Deps struct {
	Applications   ApplicationService
	Callbacks      CallbackStore
	DB             Pinger
	WebhookBanks   map[string]WebhookBank // асинхронные банки по коду
	WebhookMaxSkew time.Duration
	Log            *slog.Logger
	Now            func() time.Time // по умолчанию time.Now
}

type api struct {
	Deps
}

// NewHandler собирает роутер API.
func NewHandler(d Deps) http.Handler {
	if d.Now == nil {
		d.Now = time.Now
	}
	a := &api{Deps: d}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/applications", a.createApplication)
	mux.HandleFunc("GET /v1/applications/{id}", a.getApplication)
	mux.HandleFunc("POST /v1/webhooks/banks/{code}", a.bankWebhook)
	mux.HandleFunc("GET /healthz", a.healthz)
	mux.HandleFunc("GET /readyz", a.readyz)

	// Свои 404 и 405 у ServeMux — text/plain, а спека требует problem+json для всех ошибок.
	// Шаблон без метода менее специфичен, чем с методом, поэтому ловит только чужие методы.
	mux.HandleFunc("/", notFound)
	for path, allow := range map[string]string{
		"/v1/applications":          "POST",
		"/v1/applications/{id}":     "GET, HEAD",
		"/v1/webhooks/banks/{code}": "POST",
		"/healthz":                  "GET, HEAD",
		"/readyz":                   "GET, HEAD",
	} {
		mux.Handle(path, methodNotAllowed(allow))
	}
	return mux
}

func notFound(w http.ResponseWriter, _ *http.Request) {
	writeProblem(w, http.StatusNotFound, "Not found", "resource not found")
}

func methodNotAllowed(allow string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Allow", allow)
		writeProblem(w, http.StatusMethodNotAllowed, "Method not allowed", "allowed methods: "+allow)
	}
}
