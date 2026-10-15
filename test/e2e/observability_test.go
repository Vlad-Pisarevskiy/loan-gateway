package e2e

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/mockbank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry/telemetrytest"
)

// allBanks — три банка; eastbank отвечает 503 на первую попытку.
func allBanks(t *testing.T) options {
	return options{banks: []mockBank{
		northbankMock(mockbank.Options{}),
		eastbankMock(mockbank.Options{FailFirst: 1}, 0),
		westbankMock(t, false),
	}}
}

// waitSpans ждёт, пока завершится не меньше n спанов name (спаны воркера заканчиваются
// чуть позже записи результата, которую видит GET).
func (e *env) waitSpans(name string, n int) []sdktrace.ReadOnlySpan {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		spans := telemetrytest.Spans(e.spans, name)
		if len(spans) >= n {
			return spans
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("spans %q = %d in 5s, want %d", name, len(spans), n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestTraceFollowsApplication(t *testing.T) {
	e := start(t, allBanks(t))
	v := e.submit(nil)

	root := e.waitSpans("POST /v1/applications", 1)[0]
	if got := telemetrytest.Attr(root, "application.id"); got != v.ID {
		t.Fatalf("root application.id = %q, want %q", got, v.ID)
	}
	traceID := root.SpanContext().TraceID()

	attempts := map[string]int{}
	for _, s := range e.waitSpans("bank_request.process", 4) {
		if s.SpanContext().TraceID() != traceID || s.Parent().SpanID() != root.SpanContext().SpanID() {
			t.Errorf("%s attempt is not a child of the POST span", telemetrytest.Attr(s, "bank.code"))
		}
		attempts[telemetrytest.Attr(s, "bank.code")]++
	}
	want := map[string]int{"northbank": 1, "eastbank": 2, "westbank": 1}
	for code, n := range want {
		if attempts[code] != n {
			t.Errorf("%s attempts in trace = %d, want %d", code, attempts[code], n)
		}
	}

	callback := e.waitSpans("bank_request.callback", 1)[0]
	if callback.SpanContext().TraceID() != traceID {
		t.Fatal("westbank decision from the webhook is not in the application trace")
	}
	links := callback.Links()
	if len(links) != 1 {
		t.Fatalf("callback links = %d, want 1", len(links))
	}
	found := false
	for _, hook := range telemetrytest.Spans(e.spans, "POST /v1/webhooks/banks/{code}") {
		if hook.SpanContext().SpanID() == links[0].SpanContext.SpanID() {
			found = hook.SpanContext().TraceID() != traceID
		}
	}
	if !found {
		t.Fatal("callback span must link to the webhook span that started its own trace")
	}
}

func TestNoPersonalDataInSpans(t *testing.T) {
	e := start(t, allBanks(t))
	e.submit(nil)
	e.waitSpans("bank_request.callback", 1)
	e.waitSpans("queue.complete", 4)

	in := sampleInput()
	secrets := []string{in["full_name"].(string), "Иванов", in["phone"].(string), "9991234567", in["birth_date"].(string)}
	for _, s := range e.spans.Ended() {
		texts := []string{s.Name(), s.Status().Description}
		for _, kv := range s.Attributes() {
			texts = append(texts, kv.Value.String())
		}
		for _, ev := range s.Events() {
			texts = append(texts, ev.Name)
			for _, kv := range ev.Attributes {
				texts = append(texts, kv.Value.String())
			}
		}
		for _, l := range s.Links() {
			for _, kv := range l.Attributes {
				texts = append(texts, kv.Value.String())
			}
		}
		for _, text := range texts {
			for _, secret := range secrets {
				if strings.Contains(text, secret) {
					t.Errorf("span %q contains personal data %q in %q", s.Name(), secret, text)
				}
			}
		}
	}
}

// eventuallyExposes ждёт, пока /metrics реестра g отдаст все строки want.
func eventuallyExposes(t *testing.T, g prometheus.Gatherer, want []string) {
	t.Helper()
	srv := httptest.NewServer(telemetry.Handler(g))
	defer srv.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var missing []string
		for _, w := range want {
			if !strings.Contains(string(body), w) {
				missing = append(missing, w)
			}
		}
		if len(missing) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("/metrics misses %q in 5s:\n%s", missing, body)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestMetricsEndpoints(t *testing.T) {
	e := start(t, allBanks(t))
	e.submit(nil)

	eventuallyExposes(t, e.apiMetrics, []string{
		`loangw_http_requests_total{code="202",method="POST",route="/v1/applications"} 1`,
		`loangw_applications_total{result="created"} 1`,
		`loangw_webhooks_total{bank="westbank",result="applied"} 1`,
		`loangw_bank_decisions_total{bank="westbank",status="approved",via="webhook"} 1`,
		`go_goroutines`,
	})
	eventuallyExposes(t, e.workerMetrics, []string{
		`loangw_bank_calls_total{bank="eastbank",outcome="retry"} 1`,
		`loangw_bank_calls_total{bank="eastbank",outcome="decision"} 1`,
		`loangw_bank_calls_total{bank="westbank",outcome="accepted"} 1`,
		`loangw_bank_decisions_total{bank="northbank",status="approved",via="sync"} 1`,
		`loangw_bank_call_duration_seconds_count{bank="northbank",outcome="decision"} 1`,
		`loangw_sweeper_actions_total{action="requeued"}`,
		`loangw_worker_tasks_in_flight 0`,
		`loangw_queue_oldest_age_seconds 0`,
	})
}
