package telemetry_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry/telemetrytest"
)

func TestTraceParentRoundTrip(t *testing.T) {
	tracer, rec := telemetrytest.Tracer()
	ctx, parent := tracer.Start(t.Context(), "parent")
	tp := telemetry.TraceParent(ctx)
	if !regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-01$`).MatchString(tp) {
		t.Fatalf("traceparent = %q", tp)
	}
	_, child := tracer.Start(telemetry.WithTraceParent(context.Background(), tp), "child")
	child.End()
	parent.End()

	spans := telemetrytest.Spans(rec, "child")
	if len(spans) != 1 {
		t.Fatalf("child spans = %d, want 1", len(spans))
	}
	got, want := spans[0].Parent(), parent.SpanContext()
	if got.TraceID() != want.TraceID() || got.SpanID() != want.SpanID() {
		t.Fatalf("child parent = %s/%s, want %s/%s", got.TraceID(), got.SpanID(), want.TraceID(), want.SpanID())
	}
}

func TestTraceParentWithoutSpanIsEmpty(t *testing.T) {
	if tp := telemetry.TraceParent(t.Context()); tp != "" {
		t.Fatalf("traceparent = %q, want empty", tp)
	}
}

func TestWithTraceParentInvalidStartsNewTrace(t *testing.T) {
	tracer, rec := telemetrytest.Tracer()
	ctx, current := tracer.Start(t.Context(), "current")
	defer current.End()
	for _, tp := range []string{"", "garbage", "00-00000000000000000000000000000000-0000000000000000-01"} {
		_, s := tracer.Start(telemetry.WithTraceParent(ctx, tp), "new:"+tp)
		s.End()
		spans := telemetrytest.Spans(rec, "new:"+tp)
		if len(spans) != 1 || spans[0].Parent().IsValid() {
			t.Errorf("traceparent %q: span must start a new trace, not continue the current one", tp)
		}
	}
}
