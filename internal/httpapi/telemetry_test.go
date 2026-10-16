package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry/telemetrytest"
)

// newObservedHandler — API с метриками в отдельном реестре и записью спанов.
func newObservedHandler(t *testing.T, svc *fakeService, cb *fakeCallbacks) (http.Handler, *prometheus.Registry, *tracetest.SpanRecorder) {
	t.Helper()
	reg := prometheus.NewRegistry()
	tracer, rec := telemetrytest.Tracer()
	h := NewHandler(Deps{
		Applications:   svc,
		Callbacks:      cb,
		DB:             fakePinger{},
		WebhookBanks:   map[string]WebhookBank{"westbank": {Handler: testCallbackHandler{}, Secret: webhookSecret}},
		WebhookMaxSkew: 5 * time.Minute,
		Log:            testLogger(t),
		Now:            func() time.Time { return webhookNow },
		Metrics:        telemetry.NewMetrics(reg),
		Tracer:         tracer,
	})
	return h, reg, rec
}

// createdBy — сервис, который создаёт заявку id; replayed — заявка уже существовала.
func createdBy(id uuid.UUID, replayed bool) *fakeService {
	return &fakeService{create: func(context.Context, string, application.Input) (application.View, error) {
		return application.View{ID: id, Replayed: replayed}, nil
	}}
}

func oneSpan(t *testing.T, rec *tracetest.SpanRecorder, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	spans := telemetrytest.Spans(rec, name)
	if len(spans) != 1 {
		t.Fatalf("spans %q = %d, want 1", name, len(spans))
	}
	return spans[0]
}

func TestHTTPMetricsUseRouteTemplates(t *testing.T) {
	svc := createdBy(uuid.New(), false)
	svc.get = func(context.Context, uuid.UUID) (application.View, error) {
		return application.View{}, application.ErrNotFound
	}
	h, reg, _ := newObservedHandler(t, svc, &fakeCallbacks{})
	do(h, http.MethodPost, "/v1/applications", validBody, withKey("k"))
	do(h, http.MethodGet, "/v1/applications/"+uuid.NewString(), "", nil)
	do(h, http.MethodGet, "/wp-admin", "", nil)
	do(h, "BREW", "/v1/applications", "", nil)
	do(h, http.MethodGet, "/healthz", "", nil)

	tests := []map[string]string{
		{"route": "/v1/applications", "method": "POST", "code": "202"},
		{"route": "/v1/applications/{id}", "method": "GET", "code": "404"},
		{"route": "other", "method": "GET", "code": "404"},
		{"route": "/v1/applications", "method": "other", "code": "405"},
		{"route": "/healthz", "method": "GET", "code": "200"},
	}
	for _, labels := range tests {
		if got := telemetrytest.Value(t, reg, "loangw_http_requests_total", labels); got != 1 {
			t.Errorf("http_requests_total%v = %v, want 1", labels, got)
		}
	}
	if got := telemetrytest.Value(t, reg, "loangw_http_request_duration_seconds", map[string]string{"route": "/v1/applications", "method": "POST"}); got != 1 {
		t.Errorf("duration observations = %v, want 1", got)
	}
}

func TestApplicationsMetric(t *testing.T) {
	conflict := &fakeService{create: func(context.Context, string, application.Input) (application.View, error) {
		return application.View{}, application.ErrIdempotencyConflict
	}}
	tests := []struct {
		svc    *fakeService
		result string
	}{
		{createdBy(uuid.New(), false), telemetry.ApplicationCreated},
		{createdBy(uuid.New(), true), telemetry.ApplicationReplayed},
		{conflict, telemetry.ApplicationConflict},
	}
	for _, tt := range tests {
		h, reg, _ := newObservedHandler(t, tt.svc, &fakeCallbacks{})
		do(h, http.MethodPost, "/v1/applications", validBody, withKey("k"))
		if got := telemetrytest.Value(t, reg, "loangw_applications_total", map[string]string{"result": tt.result}); got != 1 {
			t.Errorf("applications_total{result=%s} = %v, want 1", tt.result, got)
		}
	}
}

func TestServerSpan(t *testing.T) {
	id := uuid.New()
	h, _, rec := newObservedHandler(t, createdBy(id, false), &fakeCallbacks{})
	do(h, http.MethodPost, "/v1/applications", validBody, map[string]string{
		"Idempotency-Key": "k",
		"traceparent":     "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	})
	do(h, http.MethodGet, "/healthz", "", nil)

	if n := len(rec.Ended()); n != 1 {
		t.Fatalf("spans = %d, want 1: /healthz must not be traced", n)
	}
	s := oneSpan(t, rec, "POST /v1/applications")
	if s.SpanKind() != trace.SpanKindServer {
		t.Errorf("kind = %s, want server", s.SpanKind())
	}
	if got := s.Parent().TraceID().String(); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("trace id = %s: incoming traceparent must be continued", got)
	}
	attrs := map[string]string{
		"http.request.method":       "POST",
		"http.route":                "/v1/applications",
		"http.response.status_code": "202",
		"application.id":            id.String(),
	}
	for k, want := range attrs {
		if got := telemetrytest.Attr(s, k); got != want {
			t.Errorf("attribute %s = %q, want %q", k, got, want)
		}
	}
}

func TestServerSpanMarksServerErrors(t *testing.T) {
	failing := &fakeService{create: func(context.Context, string, application.Input) (application.View, error) {
		return application.View{}, errors.New("db down")
	}}
	h, _, rec := newObservedHandler(t, failing, &fakeCallbacks{})
	do(h, http.MethodPost, "/v1/applications", validBody, withKey("k"))
	if s := oneSpan(t, rec, "POST /v1/applications"); s.Status().Code != codes.Error {
		t.Fatalf("status = %+v, want Error for 500", s.Status())
	}
}

func TestWebhookMetrics(t *testing.T) {
	approvedBody := `{"id":"w-1","approved":true}`
	badSignature := map[string]string{bank.HeaderWebhookTimestamp: "1760000000", bank.HeaderWebhookSignature: "sha256=00"}
	tests := []struct {
		name   string
		path   string
		body   string
		hdr    map[string]string
		cb     *fakeCallbacks
		labels map[string]string
	}{
		{"unknown bank", "/v1/webhooks/banks/no-such-bank", approvedBody, signedAt(webhookNow, approvedBody), &fakeCallbacks{}, map[string]string{"bank": "other", "result": "unknown_bank"}},
		{"bad signature", westPath, approvedBody, badSignature, &fakeCallbacks{}, map[string]string{"bank": "westbank", "result": "unauthorized"}},
		{"bad body", westPath, `{`, signedAt(webhookNow, `{`), &fakeCallbacks{}, map[string]string{"bank": "westbank", "result": "bad_body"}},
		{"applied", westPath, approvedBody, signedAt(webhookNow, approvedBody), &fakeCallbacks{result: application.CallbackApplied}, map[string]string{"bank": "westbank", "result": "applied"}},
		{"duplicate", westPath, approvedBody, signedAt(webhookNow, approvedBody), &fakeCallbacks{result: application.CallbackDuplicate}, map[string]string{"bank": "westbank", "result": "duplicate"}},
		{"unknown request", westPath, approvedBody, signedAt(webhookNow, approvedBody), &fakeCallbacks{result: application.CallbackUnknown}, map[string]string{"bank": "westbank", "result": "unknown"}},
		{"store error", westPath, approvedBody, signedAt(webhookNow, approvedBody), &fakeCallbacks{err: errors.New("db down")}, map[string]string{"bank": "westbank", "result": "error"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, reg, _ := newObservedHandler(t, &fakeService{}, tt.cb)
			do(h, http.MethodPost, tt.path, tt.body, tt.hdr)
			if got := telemetrytest.Value(t, reg, "loangw_webhooks_total", tt.labels); got != 1 {
				t.Fatalf("webhooks_total%v = %v, want 1", tt.labels, got)
			}
		})
	}
}

func TestWebhookDecisionMetric(t *testing.T) {
	h, reg, _ := newObservedHandler(t, &fakeService{}, &fakeCallbacks{result: application.CallbackApplied})
	body := `{"id":"w-1","approved":true}`
	do(h, http.MethodPost, westPath, body, signedAt(webhookNow, body))
	labels := map[string]string{"bank": "westbank", "status": "approved", "via": "webhook"}
	if got := telemetrytest.Value(t, reg, "loangw_bank_decisions_total", labels); got != 1 {
		t.Fatalf("bank_decisions_total%v = %v, want 1", labels, got)
	}
}

func TestWebhookCallbackSpanJoinsApplicationTrace(t *testing.T) {
	appTracer, _ := telemetrytest.Tracer()
	ctx, appSpan := appTracer.Start(t.Context(), "POST /v1/applications")
	appSpan.End()
	reqID := uuid.New()
	cb := &fakeCallbacks{result: application.CallbackApplied, requestID: reqID, traceParent: telemetry.TraceParent(ctx)}
	h, _, rec := newObservedHandler(t, &fakeService{}, cb)
	body := `{"id":"w-1","approved":true}`
	do(h, http.MethodPost, westPath, body, signedAt(webhookNow, body))

	hook := oneSpan(t, rec, "POST /v1/webhooks/banks/{code}")
	s := oneSpan(t, rec, "bank_request.callback")
	app := appSpan.SpanContext()
	if s.Parent().TraceID() != app.TraceID() || s.Parent().SpanID() != app.SpanID() {
		t.Fatalf("callback parent = %s/%s, want application span %s/%s", s.Parent().TraceID(), s.Parent().SpanID(), app.TraceID(), app.SpanID())
	}
	if hook.SpanContext().TraceID() == app.TraceID() {
		t.Fatal("webhook request must start its own trace: the bank does not send our traceparent")
	}
	if links := s.Links(); len(links) != 1 || links[0].SpanContext.SpanID() != hook.SpanContext().SpanID() {
		t.Fatalf("callback links = %+v, want one link to the webhook span", links)
	}
	attrs := map[string]string{"bank_request.id": reqID.String(), "bank.code": "westbank", "decision.status": "approved"}
	for k, want := range attrs {
		if got := telemetrytest.Attr(s, k); got != want {
			t.Errorf("attribute %s = %q, want %q", k, got, want)
		}
	}
}

func TestDuplicateWebhookAddsNoCallbackSpan(t *testing.T) {
	appTracer, _ := telemetrytest.Tracer()
	ctx, appSpan := appTracer.Start(t.Context(), "POST /v1/applications")
	appSpan.End()
	cb := &fakeCallbacks{result: application.CallbackDuplicate, traceParent: telemetry.TraceParent(ctx)}
	h, _, rec := newObservedHandler(t, &fakeService{}, cb)
	body := `{"id":"w-1","approved":true}`
	do(h, http.MethodPost, westPath, body, signedAt(webhookNow, body))
	if spans := telemetrytest.Spans(rec, "bank_request.callback"); len(spans) != 0 {
		t.Fatalf("callback spans = %d, want 0 for a duplicate", len(spans))
	}
}

// TestAPICreatesWebhookSeriesAtStart: серии вебхуков асинхронных банков видны до первого запроса.
func TestAPICreatesWebhookSeriesAtStart(t *testing.T) {
	_, reg, _ := newObservedHandler(t, &fakeService{}, &fakeCallbacks{})
	rec := httptest.NewRecorder()
	telemetry.Handler(reg).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if want := `loangw_webhooks_total{bank="westbank",result="applied"} 0`; !strings.Contains(rec.Body.String(), want) {
		t.Errorf("exposition has no %q", want)
	}
}
