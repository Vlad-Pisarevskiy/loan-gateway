package telemetry_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"go.opentelemetry.io/otel"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"
)

func TestSetupTracingDisabledWithoutEndpoint(t *testing.T) {
	shutdown, err := telemetry.SetupTracing(t.Context(), func(string) string { return "" }, "gateway-api", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shutdown(context.Background()) }()
	_, span := telemetry.Tracer().Start(t.Context(), "op")
	defer span.End()
	if span.IsRecording() {
		t.Fatal("span is recorded although OTEL_EXPORTER_OTLP_ENDPOINT is not set")
	}
}

func TestSetupTracingDoesNotBlockOnUnreachableCollector(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	prev := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	shutdown, err := telemetry.SetupTracing(t.Context(), os.Getenv, "gateway-api", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	for range 1000 {
		_, span := telemetry.Tracer().Start(t.Context(), "op")
		if !span.IsRecording() {
			t.Fatal("span is not recorded although OTEL_EXPORTER_OTLP_ENDPOINT is set")
		}
		span.End()
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("1000 spans took %s: export must not block request processing", d)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	begin := time.Now()
	_ = shutdown(ctx) // коллектор недоступен — ошибка ожидаема; важно, что остановка не висит
	if d := time.Since(begin); d > 3*time.Second {
		t.Fatalf("shutdown took %s, ignoring its 2s deadline", d)
	}
}
