package telemetry

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Shutdown досылает накопленные спаны и останавливает трейсинг в пределах ctx.
type Shutdown func(context.Context) error

// SetupTracing настраивает глобальный трейсер и пропагатор W3C Trace Context (раздел 14.4 спеки).
// Без OTEL_EXPORTER_OTLP_ENDPOINT и OTEL_EXPORTER_OTLP_TRACES_ENDPOINT трейсинг выключен:
// остаётся no-op трейсер. serviceName — имя по умолчанию, OTEL_SERVICE_NAME его переопределяет.
// Остальные переменные OTEL_* (адрес, семплер) читает SDK.
func SetupTracing(ctx context.Context, getenv func(string) string, serviceName string, log *slog.Logger) (Shutdown, error) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	if getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" && getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") == "" {
		return func(context.Context) error { return nil }, nil
	}
	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("otlp exporter: %w", err)
	}
	name := cmp.Or(getenv("OTEL_SERVICE_NAME"), serviceName)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp), // экспорт в фоне: недоступный Jaeger не тормозит заявки
		sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", name))),
	)
	otel.SetTracerProvider(tp)
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		log.Warn("telemetry export failed", "error", err)
	}))
	return tp.Shutdown, nil
}

// Tracer — трейсер шлюза из глобального провайдера.
func Tracer() trace.Tracer {
	return otel.Tracer("github.com/Vlad-Pisarevskiy/loan-gateway")
}
