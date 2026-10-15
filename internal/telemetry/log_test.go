package telemetry_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry/telemetrytest"
)

func TestLogHandlerAddsTraceID(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(telemetry.NewLogHandler(slog.NewJSONHandler(&buf, nil)))
	tracer, _ := telemetrytest.Tracer()
	ctx, span := tracer.Start(t.Context(), "op")
	defer span.End()

	log.InfoContext(ctx, "with span")
	log.With("k", "v").InfoContext(t.Context(), "without span")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("log lines = %d, want 2:\n%s", len(lines), buf.String())
	}
	var with, without map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &with); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &without); err != nil {
		t.Fatal(err)
	}
	sc := span.SpanContext()
	if with["trace_id"] != sc.TraceID().String() || with["span_id"] != sc.SpanID().String() {
		t.Fatalf("record with span = %v, want trace_id %s and span_id %s", with, sc.TraceID(), sc.SpanID())
	}
	if _, ok := without["trace_id"]; ok {
		t.Fatalf("record without span has trace_id: %v", without)
	}
	if without["k"] != "v" {
		t.Fatalf("attributes from With are lost: %v", without)
	}
}
