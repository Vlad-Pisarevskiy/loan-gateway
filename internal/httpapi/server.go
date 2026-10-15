// Package httpapi — HTTP API шлюза: заявки, вебхуки банков, health-проверки.
package httpapi

import (
	"context"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"
)

// ApplicationService — сценарии заявок; реализация — application.Service.
type ApplicationService interface {
	Create(ctx context.Context, key string, in application.Input) (application.View, error)
	Get(ctx context.Context, id uuid.UUID) (application.View, error)
}

// CallbackStore применяет решения из вебхуков; реализация — store/postgres.Queue.
type CallbackStore interface {
	ApplyCallback(ctx context.Context, bankCode, externalID string, d application.Decision) (application.CallbackApply, error)
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
	Now            func() time.Time   // по умолчанию time.Now
	Metrics        *telemetry.Metrics // nil — метрики не пишутся
	Tracer         trace.Tracer       // nil — спаны не создаются
}

type api struct {
	Deps
}

// NewHandler собирает роутер API.
func NewHandler(d Deps) http.Handler {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Tracer == nil {
		d.Tracer = noop.NewTracerProvider().Tracer("")
	}
	d.Metrics.InitAPISeries(slices.Sorted(maps.Keys(d.WebhookBanks)))
	a := &api{Deps: d}
	routes := []struct {
		method, path string
		handler      http.HandlerFunc
	}{
		{http.MethodPost, "/v1/applications", a.createApplication},
		{http.MethodGet, "/v1/applications/{id}", a.getApplication},
		{http.MethodPost, "/v1/webhooks/banks/{code}", a.bankWebhook},
		{http.MethodGet, "/healthz", a.healthz},
		{http.MethodGet, "/readyz", a.readyz},
	}
	mux := http.NewServeMux()
	allowed := map[string][]string{}
	for _, rt := range routes {
		mux.HandleFunc(rt.method+" "+rt.path, a.instrument(rt.path, rt.handler))
		allowed[rt.path] = append(allowed[rt.path], rt.method)
		if rt.method == http.MethodGet { // ServeMux отдаёт HEAD в GET-обработчик
			allowed[rt.path] = append(allowed[rt.path], http.MethodHead)
		}
	}
	// Свои 404 и 405 у ServeMux — text/plain, а спека требует problem+json для всех ошибок.
	// Шаблон без метода менее специфичен, чем с методом, поэтому ловит только чужие методы.
	mux.HandleFunc("/", a.instrument(telemetry.Other, notFound))
	for path, methods := range allowed {
		mux.Handle(path, a.instrument(path, methodNotAllowed(strings.Join(methods, ", "))))
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
