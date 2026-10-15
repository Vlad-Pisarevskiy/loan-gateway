package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

var traceContext = propagation.TraceContext{}

// TraceParent возвращает контекст текущего спана в формате W3C traceparent; "" — валидного спана нет.
func TraceParent(ctx context.Context) string {
	c := propagation.MapCarrier{}
	traceContext.Inject(ctx, c)
	return c.Get("traceparent")
}

// WithTraceParent возвращает контекст, в котором tp — родитель новых спанов. Текущий спан ctx
// родителем не станет: пустой или невалидный tp даёт новый трейс (раздел 14.4 спеки).
func WithTraceParent(ctx context.Context, tp string) context.Context {
	ctx = trace.ContextWithSpanContext(ctx, trace.SpanContext{})
	return traceContext.Extract(ctx, propagation.MapCarrier{"traceparent": tp})
}
