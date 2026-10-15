package dispatch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry/telemetrytest"
)

// newObservedWorker — воркер с метриками в отдельном реестре и записью спанов.
func newObservedWorker(t *testing.T, q Queue, banks ...bank.Bank) (*Worker, *prometheus.Registry, *tracetest.SpanRecorder) {
	t.Helper()
	reg := prometheus.NewRegistry()
	tracer, rec := telemetrytest.Tracer()
	cfg := testConfig()
	cfg.Metrics = telemetry.NewMetrics(reg)
	cfg.Tracer = tracer
	return newTestWorker(t, q, cfg, banks...), reg, rec
}

func oneSpan(t *testing.T, rec *tracetest.SpanRecorder, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	spans := telemetrytest.Spans(rec, name)
	if len(spans) != 1 {
		t.Fatalf("spans %q = %d, want 1", name, len(spans))
	}
	return spans[0]
}

func TestProcessRecordsOutcomeMetrics(t *testing.T) {
	accepted := func(context.Context, bank.SubmitRequest) (bank.SubmitResult, error) {
		return bank.SubmitResult{ExternalID: "w-1"}, nil
	}
	tests := []struct {
		submit  submitFunc
		outcome string
	}{
		{decide, "decision"},
		{accepted, "accepted"},
		{failWith(bank.StatusError(503)), "retry"},
		{failWith(bank.StatusError(400)), "failed"},
	}
	for _, tt := range tests {
		t.Run(tt.outcome, func(t *testing.T) {
			w, reg, _ := newObservedWorker(t, &fakeQueue{}, fakeBank{code: "northbank", submit: tt.submit})
			w.process(t.Context(), newTask("northbank", 1))
			labels := map[string]string{"bank": "northbank", "outcome": tt.outcome}
			if got := telemetrytest.Value(t, reg, "loangw_bank_calls_total", labels); got != 1 {
				t.Errorf("bank_calls_total%v = %v, want 1", labels, got)
			}
			if got := telemetrytest.Value(t, reg, "loangw_bank_call_duration_seconds", labels); got != 1 {
				t.Errorf("bank_call_duration observations%v = %v, want 1", labels, got)
			}
		})
	}
}

func TestProcessCountsSyncDecision(t *testing.T) {
	w, reg, _ := newObservedWorker(t, &fakeQueue{}, fakeBank{code: "northbank", submit: decide})
	w.process(t.Context(), newTask("northbank", 1))
	labels := map[string]string{"bank": "northbank", "status": "approved", "via": "sync"}
	if got := telemetrytest.Value(t, reg, "loangw_bank_decisions_total", labels); got != 1 {
		t.Fatalf("bank_decisions_total%v = %v, want 1", labels, got)
	}
}

func TestProcessUnknownBankHasNoDuration(t *testing.T) {
	// Реестр без банков не создаётся, поэтому в нём northbank, а задача — для nobank.
	w, reg, _ := newObservedWorker(t, &fakeQueue{}, fakeBank{code: "northbank", submit: decide})
	w.process(t.Context(), newTask("nobank", 1))
	labels := map[string]string{"bank": "nobank", "outcome": "failed"}
	if got := telemetrytest.Value(t, reg, "loangw_bank_calls_total", labels); got != 1 {
		t.Errorf("bank_calls_total%v = %v, want 1", labels, got)
	}
	if telemetrytest.Has(t, reg, "loangw_bank_call_duration_seconds") {
		t.Error("duration is observed although the bank was not called")
	}
}

func TestProcessCountsLostLeaseAndSaveErrors(t *testing.T) {
	w, reg, _ := newObservedWorker(t, &fakeQueue{lostLease: true}, fakeBank{code: "northbank", submit: decide})
	w.process(t.Context(), newTask("northbank", 1))
	if got := telemetrytest.Value(t, reg, "loangw_lease_lost_total", nil); got != 1 {
		t.Errorf("lease_lost_total = %v, want 1", got)
	}
	if got := telemetrytest.Value(t, reg, "loangw_bank_decisions_total", map[string]string{"bank": "northbank", "status": "approved", "via": "sync"}); got != 0 {
		t.Errorf("decision is counted although it was not saved: %v", got)
	}

	w, reg, _ = newObservedWorker(t, &fakeQueue{completeErr: errors.New("db down")}, fakeBank{code: "northbank", submit: decide})
	w.process(t.Context(), newTask("northbank", 1))
	if got := telemetrytest.Value(t, reg, "loangw_queue_errors_total", map[string]string{"op": "complete"}); got != 1 {
		t.Errorf("queue_errors_total{op=complete} = %v, want 1", got)
	}
}

func TestInFlightGauge(t *testing.T) {
	release := make(chan struct{})
	blocking := func(ctx context.Context, _ bank.SubmitRequest) (bank.SubmitResult, error) {
		<-release
		return decide(ctx, bank.SubmitRequest{})
	}
	q := &fakeQueue{pending: []Task{newTask("northbank", 1)}}
	w, reg, _ := newObservedWorker(t, q, fakeBank{code: "northbank", submit: blocking})
	runWorker(t, w)
	eventually(t, "one task in flight", func() bool {
		return telemetrytest.Value(t, reg, "loangw_worker_tasks_in_flight", nil) == 1
	})
	close(release)
	eventually(t, "no tasks in flight", func() bool {
		return len(q.completions()) == 1 && telemetrytest.Value(t, reg, "loangw_worker_tasks_in_flight", nil) == 0
	})
}

func TestRunCountsClaimErrorsAndSweeps(t *testing.T) {
	q := &fakeQueue{claimErrs: 1, sweepStats: SweepStats{CallbackTimeouts: 1, Requeued: 2, LeaseExpired: 3}}
	w, reg, _ := newObservedWorker(t, q, fakeBank{code: "northbank", submit: decide})
	runWorker(t, w)
	eventually(t, "claim error and sweeper actions counted", func() bool {
		return telemetrytest.Value(t, reg, "loangw_queue_errors_total", map[string]string{"op": "claim"}) == 1 &&
			telemetrytest.Value(t, reg, "loangw_sweeper_actions_total", map[string]string{"action": "requeued"}) >= 2
	})
}

func TestProcessSpansJoinApplicationTrace(t *testing.T) {
	w, _, rec := newObservedWorker(t, &fakeQueue{}, fakeBank{code: "eastbank", submit: failWith(bank.StatusError(503))})
	appTracer, _ := telemetrytest.Tracer()
	ctx, appSpan := appTracer.Start(t.Context(), "POST /v1/applications")
	appSpan.End()
	task := newTask("eastbank", 1)
	task.TraceParent = telemetry.TraceParent(ctx)
	w.process(t.Context(), task)

	process := oneSpan(t, rec, "bank_request.process")
	submit := oneSpan(t, rec, "bank.submit")
	complete := oneSpan(t, rec, "queue.complete")
	app := appSpan.SpanContext()
	if process.Parent().TraceID() != app.TraceID() || process.Parent().SpanID() != app.SpanID() {
		t.Fatal("bank_request.process must be a child of the span that created the application")
	}
	if process.SpanKind() != trace.SpanKindConsumer || submit.SpanKind() != trace.SpanKindClient {
		t.Errorf("kinds: process %s, submit %s; want consumer and client", process.SpanKind(), submit.SpanKind())
	}
	if submit.Parent().SpanID() != process.SpanContext().SpanID() || complete.Parent().SpanID() != process.SpanContext().SpanID() {
		t.Error("bank.submit and queue.complete must be children of bank_request.process")
	}
	want := map[string]string{
		"application.id":  task.Application.ID.String(),
		"bank_request.id": task.RequestID.String(),
		"bank.code":       "eastbank",
		"attempt":         "1",
		"outcome":         "retry",
	}
	for k, v := range want {
		if got := telemetrytest.Attr(process, k); got != v {
			t.Errorf("process attribute %s = %q, want %q", k, got, v)
		}
	}
	if telemetrytest.Attr(submit, "error.type") != "temporary" || submit.Status().Code != codes.Error {
		t.Errorf("submit: error.type %q, status %+v; want temporary and Error", telemetrytest.Attr(submit, "error.type"), submit.Status())
	}
	if got := telemetrytest.Attr(complete, "result"); got != "saved" {
		t.Errorf("complete result = %q, want saved", got)
	}
}

func TestProcessPermanentErrorType(t *testing.T) {
	w, _, rec := newObservedWorker(t, &fakeQueue{}, fakeBank{code: "northbank", submit: failWith(bank.StatusError(400))})
	w.process(t.Context(), newTask("northbank", 1))
	if got := telemetrytest.Attr(oneSpan(t, rec, "bank.submit"), "error.type"); got != "permanent" {
		t.Fatalf("error.type = %q, want permanent", got)
	}
}

func TestProcessWithoutTraceParentStartsTrace(t *testing.T) {
	for _, tp := range []string{"", "garbage"} {
		w, _, rec := newObservedWorker(t, &fakeQueue{}, fakeBank{code: "northbank", submit: decide})
		task := newTask("northbank", 1)
		task.TraceParent = tp
		w.process(t.Context(), task)
		if s := oneSpan(t, rec, "bank_request.process"); s.Parent().IsValid() {
			t.Errorf("traceparent %q: process span has a parent, want a new trace", tp)
		}
	}
}

func TestCompleteSpanResult(t *testing.T) {
	tests := []struct {
		q    *fakeQueue
		want string
	}{
		{&fakeQueue{lostLease: true}, "lease_lost"},
		{&fakeQueue{completeErr: errors.New("db down")}, "error"},
	}
	for _, tt := range tests {
		w, _, rec := newObservedWorker(t, tt.q, fakeBank{code: "northbank", submit: decide})
		w.process(t.Context(), newTask("northbank", 1))
		if got := telemetrytest.Attr(oneSpan(t, rec, "queue.complete"), "result"); got != tt.want {
			t.Errorf("complete result = %q, want %q", got, tt.want)
		}
	}
}

// TestWorkerCreatesBankSeriesAtStart: серии по банкам видны до первой задачи.
func TestWorkerCreatesBankSeriesAtStart(t *testing.T) {
	_, reg, _ := newObservedWorker(t, &fakeQueue{}, fakeBank{code: "northbank", submit: decide})
	rec := httptest.NewRecorder()
	telemetry.Handler(reg).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, want := range []string{
		`loangw_bank_calls_total{bank="northbank",outcome="failed"} 0`,
		`loangw_bank_decisions_total{bank="northbank",status="approved",via="sync"} 0`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("exposition has no %q", want)
		}
	}
}

// TestOutcomeKindsAreKnownMetricValues: значения outcome, которые пишет воркер, входят в набор,
// из которого создаются нулевые серии.
func TestOutcomeKindsAreKnownMetricValues(t *testing.T) {
	for _, k := range []OutcomeKind{OutcomeDecision, OutcomeAccepted, OutcomeRetry, OutcomeFailed} {
		if !slices.Contains(telemetry.BankOutcomes, k.String()) {
			t.Errorf("outcome %q is not in telemetry.BankOutcomes", k)
		}
	}
}
