package telemetry_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry/telemetrytest"
)

func TestMetricsRecordValues(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := telemetry.NewMetrics(reg)
	m.HTTPRequest("/v1/applications", http.MethodPost, http.StatusAccepted, 30*time.Millisecond)
	m.Application(telemetry.ApplicationReplayed)
	m.Webhook("westbank", telemetry.WebhookApplied)
	m.BankCall("eastbank", "retry")
	m.BankCallDuration("eastbank", "retry", time.Second)
	m.Decision("northbank", true, telemetry.ViaSync)
	m.Decision("westbank", false, telemetry.ViaWebhook)
	m.TaskStarted()
	m.TaskStarted()
	m.TaskFinished()
	m.QueueError(telemetry.OpClaim)
	m.LeaseLost()
	m.Swept(1, 2, 3)

	tests := []struct {
		name   string
		labels map[string]string
		want   float64
	}{
		{"loangw_http_requests_total", map[string]string{"route": "/v1/applications", "method": "POST", "code": "202"}, 1},
		{"loangw_http_request_duration_seconds", map[string]string{"route": "/v1/applications", "method": "POST"}, 1},
		{"loangw_applications_total", map[string]string{"result": "replayed"}, 1},
		{"loangw_webhooks_total", map[string]string{"bank": "westbank", "result": "applied"}, 1},
		{"loangw_bank_calls_total", map[string]string{"bank": "eastbank", "outcome": "retry"}, 1},
		{"loangw_bank_call_duration_seconds", map[string]string{"bank": "eastbank", "outcome": "retry"}, 1},
		{"loangw_bank_decisions_total", map[string]string{"bank": "northbank", "status": "approved", "via": "sync"}, 1},
		{"loangw_bank_decisions_total", map[string]string{"bank": "westbank", "status": "rejected", "via": "webhook"}, 1},
		{"loangw_worker_tasks_in_flight", nil, 1},
		{"loangw_queue_errors_total", map[string]string{"op": "claim"}, 1},
		{"loangw_lease_lost_total", nil, 1},
		{"loangw_sweeper_actions_total", map[string]string{"action": "callback_timeout"}, 1},
		{"loangw_sweeper_actions_total", map[string]string{"action": "requeued"}, 2},
		{"loangw_sweeper_actions_total", map[string]string{"action": "lease_expired"}, 3},
	}
	for _, tt := range tests {
		if got := telemetrytest.Value(t, reg, tt.name, tt.labels); got != tt.want {
			t.Errorf("%s%v = %v, want %v", tt.name, tt.labels, got, tt.want)
		}
	}
}

func TestNilMetricsIsNoop(t *testing.T) {
	var m *telemetry.Metrics
	m.HTTPRequest("/healthz", http.MethodGet, http.StatusOK, time.Millisecond)
	m.Application(telemetry.ApplicationCreated)
	m.Webhook("westbank", telemetry.WebhookApplied)
	m.BankCall("northbank", "decision")
	m.BankCallDuration("northbank", "decision", time.Millisecond)
	m.Decision("northbank", true, telemetry.ViaSync)
	m.TaskStarted()
	m.TaskFinished()
	m.QueueError(telemetry.OpSweep)
	m.LeaseLost()
	m.Swept(1, 1, 1)
	m.InitWorkerSeries([]string{"northbank"})
	m.InitAPISeries([]string{"westbank"})
}

// TestInitSeriesStartAtZero: серии создаются с нулём до первого события. Value не отличает 0 от
// отсутствия серии, поэтому наличие проверяем по тексту экспозиции.
func TestInitSeriesStartAtZero(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := telemetry.NewMetrics(reg)
	m.InitWorkerSeries([]string{"northbank"})
	m.InitAPISeries([]string{"westbank"})
	rec := httptest.NewRecorder()
	telemetry.Handler(reg).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`loangw_bank_calls_total{bank="northbank",outcome="retry"} 0`,
		`loangw_bank_decisions_total{bank="northbank",status="approved",via="sync"} 0`,
		`loangw_webhooks_total{bank="westbank",result="applied"} 0`,
		`loangw_webhooks_total{bank="other",result="unknown_bank"} 0`,
		`loangw_bank_decisions_total{bank="westbank",status="rejected",via="webhook"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("exposition has no %q", want)
		}
	}
}

func TestRegistryHasRuntimeMetrics(t *testing.T) {
	if !telemetrytest.Has(t, telemetry.NewRegistry(), "go_goroutines") {
		t.Fatal("go_goroutines is missing: the Go runtime collector is not registered")
	}
}

func TestHandlerServesMetrics(t *testing.T) {
	reg := telemetry.NewRegistry()
	telemetry.NewMetrics(reg).LeaseLost()
	rec := httptest.NewRecorder()
	telemetry.Handler(reg).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "loangw_lease_lost_total 1") {
		t.Fatalf("code %d, body:\n%s", rec.Code, rec.Body)
	}
}
