# Loan Gateway v0.2.0 — наблюдаемость: план реализации

> **Для агентов-исполнителей:** ОБЯЗАТЕЛЬНЫЙ НАВЫК: superpowers:subagent-driven-development (рекомендуется) или superpowers:executing-plans — задача за задачей. Шаги размечены чекбоксами (`- [ ]`).

**Цель:** метрики Prometheus, трейсинг OpenTelemetry, дашборд Grafana и алерты по разделу 14 спеки — релиз v0.2.0.

**Архитектура:** новый пакет `internal/telemetry` описывает метрики, настраивает трейсинг и добавляет `trace_id` в логи. `httpapi` и `dispatch` получают `*telemetry.Metrics` и `trace.Tracer` параметрами; `nil` — запись выключена. Контекст трейса заявки хранится в колонке `bank_requests.trace_parent`, поэтому один трейс проходит через очередь и вебхук. `api` и `worker` отдают `/metrics` на отдельном `METRICS_ADDR`; Prometheus, Jaeger и Grafana поднимаются в compose.

**Стек:** Go 1.26, `github.com/prometheus/client_golang` v1.24.1, `go.opentelemetry.io/otel` v1.47.0 (API, SDK, экспортёр OTLP/HTTP), Prometheus v3.15.0, Jaeger v2.21.0, Grafana 13.2.3.

**Спека:** `docs/spec.md`, раздел 14 и связанные правки разделов 2, 3, 5.4, 7, 9, 10, 12. План опирается на неё; при расхождении права спека — остановиться и спросить.

## Global Constraints

- Go 1.26 (`go 1.26.0` в `go.mod`), модуль `github.com/Vlad-Pisarevskiy/loan-gateway`.
- Новые зависимости — ровно эти и этих версий: `github.com/prometheus/client_golang` v1.24.1; `go.opentelemetry.io/otel`, `go.opentelemetry.io/otel/trace`, `go.opentelemetry.io/otel/sdk`, `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp` — v1.47.0. Не добавлять `otelhttp`, gRPC-экспортёр, пакеты `semconv` и OpenTelemetry Collector.
- Образы compose: `prom/prometheus:v3.15.0`, `jaegertracing/jaeger:2.21.0`, `grafana/grafana:13.2.3`.
- Имена, типы и метки метрик — ровно как в таблице 14.2 спеки, префикс `loangw_`. Значения меток — только из ограниченных наборов; неизвестные путь, метод и банк вебхука → `other`. Идентификаторов и ПДн в метках нет.
- Имена, виды и атрибуты спанов — ровно как в таблице 14.4 спеки. ФИО, телефон, дата рождения, суммы анкеты и тела запросов в атрибуты не попадают. Вызовы банков не получают заголовок `traceparent`.
- Трейсинг включён, только если задан `OTEL_EXPORTER_OTLP_ENDPOINT` или `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`; иначе no-op трейсер.
- `METRICS_ADDR` по умолчанию `:9100`; `/metrics` не отдаётся на `HTTP_ADDR`.
- Методы `*telemetry.Metrics` безопасны для `nil`; тесты, которым метрики не нужны, передают `nil`.
- Прежние правила v0.1.0 действуют: деньги — `int64` в копейках; ФИО, телефон и дата рождения не попадают в логи и тексты ошибок; каждый `UPDATE bank_requests` из воркера содержит `status = 'sending' AND attempts = $claimed`; ошибки адаптеров — `bank.ErrTemporary` / `bank.ErrPermanent`.
- Комментарии в коде — на русском; строки ошибок и сообщения логов — на английском, с маленькой буквы.
- Интеграционные и e2e-тесты пропускаются при `go test -short`.
- Gitflow: работа только в `feature/<slug>` от `develop`, PR — в `develop`; `main` — только релизы с тегом `vX.Y.Z`. Коммиты — Conventional Commits на английском.

## Review Focus

Входы и сбои, которые спека подразумевает, но легко упустить. Для каждого в указанной задаче есть закрепляющий тест.

1. **Сканер шлёт произвольные методы и пути** (`BREW /v1/applications`, `GET /wp-admin`). Метки `method="other"` и `route="other"`, число серий не растёт. Тест: Task 3 (`TestHTTPMetricsUseRouteTemplates`).
2. **Вебхук на несуществующий код банка** (`/v1/webhooks/banks/<что угодно>`). Метка `bank="other"`, а не код из пути. Тест: Task 3 (`TestWebhookMetrics`).
3. **Запросы без контекста трейса или с испорченным:** созданные до миграции v0.2.0 (`trace_parent IS NULL`) или с мусором в колонке. Воркер обрабатывает их как обычно, спан `bank_request.process` становится корнем нового трейса. Тесты: Task 2 (`TestCreateWithoutSpanLeavesTraceParentEmpty`), Task 4 (`TestProcessWithoutTraceParentStartsTrace`).
4. **Jaeger недоступен.** Создание спанов не тормозит обработку, остановка не висит дольше своего дедлайна. Тест: Task 1 (`TestSetupTracingDoesNotBlockOnUnreachableCollector`).
5. **БД зависла во время опроса `/metrics`.** Ответ приходит в пределах таймаута, gauge очереди пропадают, растёт `loangw_queue_errors_total{op="stats"}`. Тесты: Task 1 (`TestQueueStatsDoesNotHangScrape`, `TestQueueStatsErrorDropsGauges`).

---

## Процесс выполнения задачи

Как в `docs/plan.md`, раздел «Процесс выполнения задачи», с одним отличием: **merge в `develop` и `main` делает человек.** Автоматическая защита Claude Code не даёт агенту мержить PR, поэтому шаг 7 выполняет человек — кнопкой в GitHub или `! gh pr merge N --merge`.

1. **Ветка:** `git switch develop && git pull --ff-only && git switch -c feature/<slug>`.
2. **Реализация:** роль `developer` проходит шаги задачи по порядку, коммитит после каждого зелёного цикла.
3. **Проверка:** `make lint && make test` — зелёные.
4. **PR:** `git push -u origin feature/<slug> && gh pr create --base develop --title "<заголовок>" --body "Task N из docs/plan-observability.md"`.
5. **Ревью:** роль `reviewer` смотрит `git diff develop...HEAD`; замечания `blocker` и `major` исправляются коммитами в ту же ветку.
6. **Журнал:** строка в `docs/dev-log.md`: дата, «v0.2.0 Task N: <название>», где, PR, замечания (исправлено / всего), стоимость. Коммит `docs: log v0.2.0 task N`, push.
7. **Merge** — человек, после зелёного CI. Затем `git switch develop && git pull --ff-only`.

### Облачные сессии

Как в `docs/plan.md`, раздел «Облачные сессии»: Docker поднимает хук `SessionStart`, PR создаётся через REST (`gh api repos/Vlad-Pisarevskiy/loan-gateway/pulls ...`), merge не делать. **Стоимость** в журнале — `?`; после сессии человек вписывает её стоимость в строку задачи (для v0.1.0 была известна только общая сумма).

## Этапы

| Этап | Задачи | Где |
|---|---|---|
| 1. Основа | Task 1, Task 2 | локально, последовательно |
| 2. Встраивание | Task 3, Task 4, Task 5 | облако, три параллельные сессии после merge Task 2 |
| 3. Сборка и релиз | Task 6, Task 7 | локально, после merge Task 3–5 |

Task 3 трогает `internal/httpapi` и `internal/application`, Task 4 — `internal/dispatch`, Task 5 — `compose.yaml`, `deploy/`, `scripts/demo.sh`, `Makefile`, CI и `CLAUDE.md`. Пересечений нет, конфликтов при merge не ожидается.

## Карта файлов

```
internal/telemetry/metrics.go          метрики 14.2, реестр, HTTP-обработчик /metrics       (Task 1)
internal/telemetry/queue.go            gauge очереди из БД: коллектор с таймаутом             (Task 1)
internal/telemetry/tracing.go          SetupTracing, Tracer: OTLP/HTTP, W3C Trace Context     (Task 1)
internal/telemetry/traceparent.go      контекст спана ↔ строка traceparent                     (Task 1)
internal/telemetry/log.go              обёртка slog.Handler: trace_id и span_id                (Task 1)
internal/telemetry/telemetrytest/      помощники тестов: значения метрик, запись спанов        (Task 1)
internal/store/postgres/migrations/00002_trace_parent.sql  колонка trace_parent               (Task 2)
internal/store/postgres/applications.go   Create пишет trace_parent                           (Task 2)
internal/store/postgres/queue.go       Claim и ApplyCallback отдают trace_parent; Stats        (Task 2)
internal/application/types.go          CallbackApply (Task 2); View.Replayed (Task 3)
internal/dispatch/types.go             Task.TraceParent                                        (Task 2)
internal/httpapi/instrument.go         обёртка маршрутов: серверный спан и метрики HTTP       (Task 3)
internal/httpapi/webhooks.go           метрики вебхуков, спан bank_request.callback           (Task 3)
internal/dispatch/worker.go            метрики и спаны воркера                                 (Task 4)
deploy/observability/                  prometheus.yml, alerts.yml, тесты алертов, Grafana     (Task 5)
cmd/gateway/metrics.go                 сервер /metrics                                         (Task 6)
internal/config/config.go              METRICS_ADDR                                            (Task 6)
test/e2e/observability_test.go         трейс заявки, отсутствие ПДн, /metrics                  (Task 7)
```

---

### Task 1: Пакет `internal/telemetry`

**Ветка:** `feature/telemetry` · **Этап 1, локально** · **Спека:** 14.1, 14.2, 14.4, 14.5

**Files:**
- Create: `internal/telemetry/metrics.go`, `internal/telemetry/queue.go`, `internal/telemetry/tracing.go`, `internal/telemetry/traceparent.go`, `internal/telemetry/log.go`, `internal/telemetry/telemetrytest/telemetrytest.go`
- Test: `internal/telemetry/metrics_test.go`, `internal/telemetry/queue_test.go`, `internal/telemetry/tracing_test.go`, `internal/telemetry/traceparent_test.go`, `internal/telemetry/log_test.go`
- Modify: `go.mod`, `go.sum`, `CLAUDE.md`

**Interfaces:**
- Consumes: —
- Produces:
  - константы меток: `ApplicationCreated`, `ApplicationReplayed`, `ApplicationConflict`; `WebhookApplied`, `WebhookDuplicate`, `WebhookUnknown`, `WebhookUnauthorized`, `WebhookBadBody`, `WebhookUnknownBank`, `WebhookError`; `ViaSync`, `ViaWebhook`; `OpClaim`, `OpComplete`, `OpSweep`, `OpStats`; `Other = "other"`
  - `type Metrics struct`; `func NewMetrics(reg prometheus.Registerer) *Metrics`; методы (все безопасны для `nil`): `HTTPRequest(route, method string, code int, d time.Duration)`, `Application(result string)`, `Webhook(bank, result string)`, `BankCall(bank, outcome string)`, `BankCallDuration(bank, outcome string, d time.Duration)`, `Decision(bank string, approved bool, via string)`, `TaskStarted()`, `TaskFinished()`, `QueueError(op string)`, `LeaseLost()`, `Swept(callbackTimeouts, requeued, leaseExpired int64)`, `RegisterQueueStats(reg prometheus.Registerer, stats QueueStatsFunc, timeout time.Duration)`
  - `func NewRegistry() *prometheus.Registry`; `func Handler(g prometheus.Gatherer) http.Handler`
  - `type QueueCount struct { Bank, Status string; Count int64 }`; `type QueueStats struct { Counts []QueueCount; OldestAge time.Duration }`; `type QueueStatsFunc func(ctx context.Context) (QueueStats, error)`
  - `type Shutdown func(context.Context) error`; `func SetupTracing(ctx context.Context, getenv func(string) string, serviceName string, log *slog.Logger) (Shutdown, error)`; `func Tracer() trace.Tracer`
  - `func TraceParent(ctx context.Context) string`; `func WithTraceParent(ctx context.Context, tp string) context.Context`
  - `func NewLogHandler(h slog.Handler) slog.Handler`
  - пакет `telemetrytest`: `Value(t testing.TB, g prometheus.Gatherer, name string, labels map[string]string) float64`, `Has(t testing.TB, g prometheus.Gatherer, name string) bool`, `Tracer() (trace.Tracer, *tracetest.SpanRecorder)`, `Spans(rec *tracetest.SpanRecorder, name string) []sdktrace.ReadOnlySpan`, `Attr(s sdktrace.ReadOnlySpan, key string) string`

- [ ] **Step 1: Создать ветку и добавить зависимости**

```bash
git switch develop && git pull --ff-only && git switch -c feature/telemetry
go get github.com/prometheus/client_golang@v1.24.1 \
  go.opentelemetry.io/otel@v1.47.0 go.opentelemetry.io/otel/trace@v1.47.0 \
  go.opentelemetry.io/otel/sdk@v1.47.0 \
  go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp@v1.47.0
```

Expected: `go.mod` получил эти модули в `require`.

- [ ] **Step 2: Написать помощники тестов**

Файл: `internal/telemetry/telemetrytest/telemetrytest.go`
```go
// Package telemetrytest — помощники тестов для метрик и трейсов.
package telemetrytest

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// Value возвращает значение серии name ровно с такими метками: для counter и gauge —
// значение, для histogram — число наблюдений. Серии нет — 0.
func Value(t testing.TB, g prometheus.Gatherer, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := g.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			if len(m.GetLabel()) != len(labels) {
				continue
			}
			match := true
			for _, l := range m.GetLabel() {
				if v, ok := labels[l.GetName()]; !ok || v != l.GetValue() {
					match = false
				}
			}
			if !match {
				continue
			}
			switch {
			case m.GetCounter() != nil:
				return m.GetCounter().GetValue()
			case m.GetGauge() != nil:
				return m.GetGauge().GetValue()
			case m.GetHistogram() != nil:
				return float64(m.GetHistogram().GetSampleCount())
			}
		}
	}
	return 0
}

// Has сообщает, есть ли хотя бы одна серия name.
func Has(t testing.TB, g prometheus.Gatherer, name string) bool {
	t.Helper()
	families, err := g.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, f := range families {
		if f.GetName() == name && len(f.GetMetric()) > 0 {
			return true
		}
	}
	return false
}

// Tracer возвращает трейсер, который записывает завершённые спаны в рекордер.
func Tracer() (trace.Tracer, *tracetest.SpanRecorder) {
	rec := tracetest.NewSpanRecorder()
	return sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)).Tracer("test"), rec
}

// Spans возвращает завершённые спаны с именем name.
func Spans(rec *tracetest.SpanRecorder, name string) []sdktrace.ReadOnlySpan {
	var out []sdktrace.ReadOnlySpan
	for _, s := range rec.Ended() {
		if s.Name() == name {
			out = append(out, s)
		}
	}
	return out
}

// Attr возвращает значение атрибута key строкой; "" — атрибута нет.
func Attr(s sdktrace.ReadOnlySpan, key string) string {
	for _, kv := range s.Attributes() {
		if string(kv.Key) == key {
			return kv.Value.Emit()
		}
	}
	return ""
}
```

- [ ] **Step 3: Написать падающие тесты метрик**

Файл: `internal/telemetry/metrics_test.go`
```go
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
```

- [ ] **Step 4: Запустить — тесты не компилируются**

Run: `go test ./internal/telemetry/`
Expected: FAIL — `undefined: telemetry.NewMetrics` и т. п.

- [ ] **Step 5: Реализовать метрики**

Файл: `internal/telemetry/metrics.go`
```go
// Package telemetry — наблюдаемость шлюза: метрики Prometheus, трейсинг OpenTelemetry и
// trace_id в логах (раздел 14 спеки).
package telemetry

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Значения меток из раздела 14.2 спеки.
const (
	ApplicationCreated  = "created"
	ApplicationReplayed = "replayed"
	ApplicationConflict = "conflict"

	WebhookApplied      = "applied"
	WebhookDuplicate    = "duplicate"
	WebhookUnknown      = "unknown"
	WebhookUnauthorized = "unauthorized"
	WebhookBadBody      = "bad_body"
	WebhookUnknownBank  = "unknown_bank"
	WebhookError        = "error"

	ViaSync    = "sync"
	ViaWebhook = "webhook"

	OpClaim    = "claim"
	OpComplete = "complete"
	OpSweep    = "sweep"
	OpStats    = "stats"

	// Other — значение метки для путей, методов и банков вне известного набора:
	// без него произвольные значения из запросов плодили бы серии.
	Other = "other"
)

// Metrics — метрики шлюза. Все методы безопасны для nil: тогда ничего не пишется.
type Metrics struct {
	httpRequests     *prometheus.CounterVec
	httpDuration     *prometheus.HistogramVec
	applications     *prometheus.CounterVec
	webhooks         *prometheus.CounterVec
	bankCalls        *prometheus.CounterVec
	bankCallDuration *prometheus.HistogramVec
	decisions        *prometheus.CounterVec
	inFlight         prometheus.Gauge
	queueErrors      *prometheus.CounterVec
	leaseLost        prometheus.Counter
	sweeper          *prometheus.CounterVec
}

// NewMetrics создаёт метрики и регистрирует их в reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "loangw_http_requests_total", Help: "HTTP API responses.",
		}, []string{"route", "method", "code"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "loangw_http_request_duration_seconds", Help: "HTTP API request duration.", Buckets: prometheus.DefBuckets,
		}, []string{"route", "method"}),
		applications: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "loangw_applications_total", Help: "Application create requests by result.",
		}, []string{"result"}),
		webhooks: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "loangw_webhooks_total", Help: "Bank webhooks by result.",
		}, []string{"bank", "result"}),
		bankCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "loangw_bank_calls_total", Help: "Processed bank requests by outcome.",
		}, []string{"bank", "outcome"}),
		bankCallDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "loangw_bank_call_duration_seconds", Help: "Bank Submit call duration.", Buckets: prometheus.DefBuckets,
		}, []string{"bank", "outcome"}),
		decisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "loangw_bank_decisions_total", Help: "Stored bank decisions.",
		}, []string{"bank", "status", "via"}),
		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "loangw_worker_tasks_in_flight", Help: "Bank calls in progress.",
		}),
		queueErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "loangw_queue_errors_total", Help: "Failed queue operations.",
		}, []string{"op"}),
		leaseLost: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "loangw_lease_lost_total", Help: "Bank results dropped because the lease was taken over.",
		}),
		sweeper: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "loangw_sweeper_actions_total", Help: "Bank requests changed by the sweeper.",
		}, []string{"action"}),
	}
	reg.MustRegister(m.httpRequests, m.httpDuration, m.applications, m.webhooks, m.bankCalls,
		m.bankCallDuration, m.decisions, m.inFlight, m.queueErrors, m.leaseLost, m.sweeper)
	return m
}

// NewRegistry возвращает реестр со стандартными метриками рантайма Go и процесса.
func NewRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return reg
}

// Handler отдаёт метрики g в текстовом формате Prometheus.
func Handler(g prometheus.Gatherer) http.Handler {
	return promhttp.HandlerFor(g, promhttp.HandlerOpts{})
}

// HTTPRequest учитывает ответ API; route — шаблон маршрута или Other.
func (m *Metrics) HTTPRequest(route, method string, code int, d time.Duration) {
	if m == nil {
		return
	}
	m.httpRequests.WithLabelValues(route, method, strconv.Itoa(code)).Inc()
	m.httpDuration.WithLabelValues(route, method).Observe(d.Seconds())
}

// Application учитывает запрос на создание заявки: ApplicationCreated, ApplicationReplayed или ApplicationConflict.
func (m *Metrics) Application(result string) {
	if m == nil {
		return
	}
	m.applications.WithLabelValues(result).Inc()
}

// Webhook учитывает вебхук; bank — код включённого асинхронного банка или Other.
func (m *Metrics) Webhook(bank, result string) {
	if m == nil {
		return
	}
	m.webhooks.WithLabelValues(bank, result).Inc()
}

// BankCall учитывает обработанную задачу с исходом outcome (decision, accepted, retry, failed).
func (m *Metrics) BankCall(bank, outcome string) {
	if m == nil {
		return
	}
	m.bankCalls.WithLabelValues(bank, outcome).Inc()
}

// BankCallDuration учитывает длительность вызова Submit.
func (m *Metrics) BankCallDuration(bank, outcome string, d time.Duration) {
	if m == nil {
		return
	}
	m.bankCallDuration.WithLabelValues(bank, outcome).Observe(d.Seconds())
}

// Decision учитывает записанное решение банка; via — ViaSync или ViaWebhook.
func (m *Metrics) Decision(bank string, approved bool, via string) {
	if m == nil {
		return
	}
	status := "rejected"
	if approved {
		status = "approved"
	}
	m.decisions.WithLabelValues(bank, status, via).Inc()
}

// TaskStarted отмечает начало вызова банка.
func (m *Metrics) TaskStarted() {
	if m == nil {
		return
	}
	m.inFlight.Inc()
}

// TaskFinished отмечает конец вызова банка.
func (m *Metrics) TaskFinished() {
	if m == nil {
		return
	}
	m.inFlight.Dec()
}

// QueueError учитывает ошибку обращения к очереди: OpClaim, OpComplete, OpSweep или OpStats.
func (m *Metrics) QueueError(op string) {
	if m == nil {
		return
	}
	m.queueErrors.WithLabelValues(op).Inc()
}

// LeaseLost учитывает результат, отброшенный из-за перехваченной аренды.
func (m *Metrics) LeaseLost() {
	if m == nil {
		return
	}
	m.leaseLost.Inc()
}

// Swept учитывает итоги прохода чистильщика.
func (m *Metrics) Swept(callbackTimeouts, requeued, leaseExpired int64) {
	if m == nil {
		return
	}
	m.sweeper.WithLabelValues("callback_timeout").Add(float64(callbackTimeouts))
	m.sweeper.WithLabelValues("requeued").Add(float64(requeued))
	m.sweeper.WithLabelValues("lease_expired").Add(float64(leaseExpired))
}
```

- [ ] **Step 6: Запустить тесты метрик**

Run: `go test ./internal/telemetry/ -run 'Metrics|Registry|Handler'`
Expected: PASS.

- [ ] **Step 7: Написать падающие тесты gauge очереди**

Файл: `internal/telemetry/queue_test.go`
```go
package telemetry_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry/telemetrytest"
)

func TestQueueStatsGauges(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := telemetry.NewMetrics(reg)
	m.RegisterQueueStats(reg, func(context.Context) (telemetry.QueueStats, error) {
		return telemetry.QueueStats{
			Counts: []telemetry.QueueCount{
				{Bank: "eastbank", Status: "queued", Count: 3},
				{Bank: "westbank", Status: "waiting_callback", Count: 1},
			},
			OldestAge: 90 * time.Second,
		}, nil
	}, time.Second)

	if got := telemetrytest.Value(t, reg, "loangw_bank_requests", map[string]string{"bank": "eastbank", "status": "queued"}); got != 3 {
		t.Errorf("eastbank queued = %v, want 3", got)
	}
	if got := telemetrytest.Value(t, reg, "loangw_bank_requests", map[string]string{"bank": "westbank", "status": "waiting_callback"}); got != 1 {
		t.Errorf("westbank waiting_callback = %v, want 1", got)
	}
	if got := telemetrytest.Value(t, reg, "loangw_queue_oldest_age_seconds", nil); got != 90 {
		t.Errorf("oldest age = %v, want 90", got)
	}
}

func TestQueueStatsErrorDropsGauges(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := telemetry.NewMetrics(reg)
	m.RegisterQueueStats(reg, func(context.Context) (telemetry.QueueStats, error) {
		return telemetry.QueueStats{}, errors.New("connection refused")
	}, time.Second)

	if telemetrytest.Has(t, reg, "loangw_bank_requests") || telemetrytest.Has(t, reg, "loangw_queue_oldest_age_seconds") {
		t.Fatal("queue gauges are exported although stats failed")
	}
	// Каждый сбор метрик повторяет запрос, поэтому счётчик не меньше 1.
	if got := telemetrytest.Value(t, reg, "loangw_queue_errors_total", map[string]string{"op": "stats"}); got < 1 {
		t.Fatalf("queue_errors{op=stats} = %v, want >= 1", got)
	}
}

func TestQueueStatsDoesNotHangScrape(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := telemetry.NewMetrics(reg)
	m.RegisterQueueStats(reg, func(ctx context.Context) (telemetry.QueueStats, error) {
		<-ctx.Done() // как запрос к зависшей БД
		return telemetry.QueueStats{}, ctx.Err()
	}, 50*time.Millisecond)

	start := time.Now()
	if _, err := reg.Gather(); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("scrape took %s: stats query must respect its timeout", d)
	}
	if telemetrytest.Has(t, reg, "loangw_queue_oldest_age_seconds") {
		t.Fatal("oldest age is exported although stats timed out")
	}
}
```

- [ ] **Step 8: Запустить — не компилируется**

Run: `go test ./internal/telemetry/ -run QueueStats`
Expected: FAIL — `undefined: telemetry.QueueStats`, `m.RegisterQueueStats undefined`.

- [ ] **Step 9: Реализовать коллектор очереди**

Файл: `internal/telemetry/queue.go`
```go
package telemetry

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// QueueCount — число незавершённых запросов в банк Bank в статусе Status.
type QueueCount struct {
	Bank   string
	Status string
	Count  int64
}

// QueueStats — снимок очереди для gauge-метрик (раздел 14.2 спеки).
type QueueStats struct {
	Counts    []QueueCount  // по банкам и статусам queued, sending, waiting_callback
	OldestAge time.Duration // возраст самой старой готовой задачи; 0 — таких нет
}

// QueueStatsFunc читает состояние очереди; реализация — store/postgres.Queue.Stats.
type QueueStatsFunc func(ctx context.Context) (QueueStats, error)

// RegisterQueueStats регистрирует gauge очереди: при каждом сборе метрик вызывается stats
// с таймаутом timeout. При ошибке gauge не отдаются, растёт loangw_queue_errors_total{op="stats"}.
// m не должен быть nil.
func (m *Metrics) RegisterQueueStats(reg prometheus.Registerer, stats QueueStatsFunc, timeout time.Duration) {
	reg.MustRegister(&queueCollector{
		stats:   stats,
		timeout: timeout,
		failed:  func() { m.QueueError(OpStats) },
		requests: prometheus.NewDesc("loangw_bank_requests",
			"Unfinished bank requests by bank and status.", []string{"bank", "status"}, nil),
		oldest: prometheus.NewDesc("loangw_queue_oldest_age_seconds",
			"Age of the oldest queued request that is ready to send.", nil, nil),
	})
}

// queueCollector читает gauge очереди из БД при каждом сборе метрик.
type queueCollector struct {
	stats    QueueStatsFunc
	timeout  time.Duration
	failed   func()
	requests *prometheus.Desc
	oldest   *prometheus.Desc
}

func (c *queueCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.requests
	ch <- c.oldest
}

func (c *queueCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	s, err := c.stats(ctx)
	if err != nil {
		c.failed()
		return
	}
	for _, q := range s.Counts {
		ch <- prometheus.MustNewConstMetric(c.requests, prometheus.GaugeValue, float64(q.Count), q.Bank, q.Status)
	}
	ch <- prometheus.MustNewConstMetric(c.oldest, prometheus.GaugeValue, s.OldestAge.Seconds())
}
```

- [ ] **Step 10: Запустить тесты очереди**

Run: `go test ./internal/telemetry/ -run 'QueueStats|Metrics|Registry|Handler'`
Expected: PASS.

- [ ] **Step 11: Написать падающие тесты traceparent и логов**

Файл: `internal/telemetry/traceparent_test.go`
```go
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
```

Файл: `internal/telemetry/log_test.go`
```go
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
```

- [ ] **Step 12: Запустить — не компилируется**

Run: `go test ./internal/telemetry/ -run 'TraceParent|LogHandler'`
Expected: FAIL — `undefined: telemetry.TraceParent`, `undefined: telemetry.NewLogHandler`.

- [ ] **Step 13: Реализовать traceparent и обёртку логов**

Файл: `internal/telemetry/traceparent.go`
```go
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
```

Файл: `internal/telemetry/log.go`
```go
package telemetry

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// NewLogHandler оборачивает h: записи с валидным спаном в контексте получают trace_id и span_id
// (раздел 14.5 спеки).
func NewLogHandler(h slog.Handler) slog.Handler {
	return traceHandler{h}
}

type traceHandler struct {
	slog.Handler
}

func (h traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r = r.Clone()
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	return h.Handler.Handle(ctx, r)
}

func (h traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceHandler{h.Handler.WithAttrs(attrs)}
}

func (h traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{h.Handler.WithGroup(name)}
}
```

- [ ] **Step 14: Запустить тесты traceparent и логов**

Run: `go test ./internal/telemetry/ -run 'TraceParent|LogHandler'`
Expected: PASS.

- [ ] **Step 15: Написать падающие тесты настройки трейсинга**

Файл: `internal/telemetry/tracing_test.go`
```go
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
```

- [ ] **Step 16: Запустить — не компилируется**

Run: `go test ./internal/telemetry/ -run SetupTracing`
Expected: FAIL — `undefined: telemetry.SetupTracing`.

- [ ] **Step 17: Реализовать настройку трейсинга**

Экспортёр — OTLP/HTTP: обычный HTTP на порту 4318 проще отлаживать и пропускать через прокси, чем gRPC. По весу зависимостей варианты равны: HTTP-экспортёр тоже подтягивает `google.golang.org/grpc` через сгенерированные типы OTLP.

Файл: `internal/telemetry/tracing.go`
```go
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
```

- [ ] **Step 18: Запустить все тесты пакета и привести go.mod в порядок**

```bash
go mod tidy
go test -race ./internal/telemetry/...
```

Expected: PASS. В `go.mod` новые модули стоят в прямом `require` (без `// indirect`).

- [ ] **Step 19: Обновить карту пакетов в CLAUDE.md**

В `CLAUDE.md`, раздел «Карта пакетов», после строки про `internal/config` добавить:

```markdown
- `internal/telemetry` — метрики Prometheus, трейсинг OpenTelemetry, `trace_id` в логах (раздел 14 спеки); `telemetrytest` — помощники тестов
```

- [ ] **Step 20: Проверка и commit**

```bash
make lint && make test
git add go.mod go.sum internal/telemetry CLAUDE.md
git commit -m "feat(telemetry): add metrics, tracing setup and trace-aware logging"
```

Expected: `make lint` — 0 issues, `make test` — PASS.

---

### Task 2: `trace_parent` в хранилище и gauge очереди

**Ветка:** `feature/trace-store` · **Этап 1, локально, после merge Task 1** · **Спека:** 7, 14.2, 14.4

**Files:**
- Create: `internal/store/postgres/migrations/00002_trace_parent.sql`
- Modify: `internal/store/postgres/applications.go`, `internal/store/postgres/queue.go`, `internal/dispatch/types.go`, `internal/application/types.go`, `internal/httpapi/server.go`, `internal/httpapi/webhooks.go`
- Test: `internal/store/postgres/applications_test.go`, `internal/store/postgres/queue_test.go`, `internal/httpapi/webhooks_test.go`

**Interfaces:**
- Consumes: `telemetry.TraceParent(ctx) string`, `telemetry.QueueStats`, `telemetry.QueueCount` (Task 1); `telemetrytest.Tracer()` (Task 1)
- Produces:
  - колонка `bank_requests.trace_parent text` (nullable)
  - `dispatch.Task.TraceParent string` — контекст трейса заявки; `""` — нет
  - `type application.CallbackApply struct { Result CallbackResult; RequestID uuid.UUID; TraceParent string }`
  - `func (q *postgres.Queue) ApplyCallback(ctx context.Context, bankCode, externalID string, d application.Decision) (application.CallbackApply, error)`
  - `httpapi.CallbackStore.ApplyCallback(...) (application.CallbackApply, error)`
  - `func (q *postgres.Queue) Stats(ctx context.Context) (telemetry.QueueStats, error)` — подходит как `telemetry.QueueStatsFunc`
  - `postgres.Applications.Create` записывает `telemetry.TraceParent(ctx)` в `trace_parent` каждого запроса (пустая строка → `NULL`)

- [ ] **Step 1: Создать ветку**

```bash
git switch develop && git pull --ff-only && git switch -c feature/trace-store
```

- [ ] **Step 2: Написать падающие интеграционные тесты**

В `internal/store/postgres/applications_test.go` добавить в импорты `"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"` и `"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry/telemetrytest"`, затем тесты:
```go
func TestCreateStoresTraceParent(t *testing.T) {
	pool := pgtest.NewPool(t)
	tracer, _ := telemetrytest.Tracer()
	ctx, span := tracer.Start(t.Context(), "POST /v1/applications")
	defer span.End()
	app := newApp()
	if _, _, err := postgres.NewApplications(pool).Create(ctx, "key-tp", []byte("h"), app, []string{"eastbank", "northbank"}); err != nil {
		t.Fatal(err)
	}
	want := telemetry.TraceParent(ctx)
	if n := count(t, pool, `SELECT count(*) FROM bank_requests WHERE application_id = $1 AND trace_parent = $2`, app.ID, want); n != 2 {
		t.Fatalf("requests with trace_parent %q = %d, want 2", want, n)
	}
}

func TestCreateWithoutSpanLeavesTraceParentEmpty(t *testing.T) {
	pool := pgtest.NewPool(t)
	app := seed(t, pool, "northbank")
	if n := count(t, pool, `SELECT count(*) FROM bank_requests WHERE application_id = $1 AND trace_parent IS NULL`, app.ID); n != 1 {
		t.Fatalf("requests without trace_parent = %d, want 1", n)
	}
	task := claim(t, postgres.NewQueue(pool), 1)[0]
	if task.TraceParent != "" {
		t.Fatalf("TraceParent = %q, want empty for NULL", task.TraceParent)
	}
}
```

В `internal/store/postgres/queue_test.go` добавить в импорты `"reflect"`, `"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"` и `"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry/telemetrytest"`, затем тесты:
```go
// seedTraced создаёт заявку в контексте спана и возвращает её traceparent.
func seedTraced(t *testing.T, pool *pgxpool.Pool, codes ...string) (application.Application, string) {
	t.Helper()
	tracer, _ := telemetrytest.Tracer()
	ctx, span := tracer.Start(t.Context(), "POST /v1/applications")
	defer span.End()
	app := newApp()
	if _, _, err := postgres.NewApplications(pool).Create(ctx, app.ID.String(), []byte("h"), app, codes); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return app, telemetry.TraceParent(ctx)
}

func TestClaimReturnsTraceParent(t *testing.T) {
	pool := pgtest.NewPool(t)
	_, tp := seedTraced(t, pool, "northbank")
	task := claim(t, postgres.NewQueue(pool), 1)[0]
	if task.TraceParent != tp {
		t.Fatalf("TraceParent = %q, want %q", task.TraceParent, tp)
	}
}

func TestApplyCallbackReturnsRequestAndTraceParent(t *testing.T) {
	pool := pgtest.NewPool(t)
	q := postgres.NewQueue(pool)
	_, tp := seedTraced(t, pool, "westbank")
	task := claim(t, q, 1)[0]
	complete(t, q, task, dispatch.Outcome{Kind: dispatch.OutcomeAccepted, ExternalID: "w-tp", CallbackTimeout: time.Minute})

	res, err := q.ApplyCallback(t.Context(), "westbank", "w-tp", approved)
	if err != nil {
		t.Fatal(err)
	}
	want := application.CallbackApply{Result: application.CallbackApplied, RequestID: task.RequestID, TraceParent: tp}
	if res != want {
		t.Fatalf("ApplyCallback = %+v, want %+v", res, want)
	}
	res, err = q.ApplyCallback(t.Context(), "westbank", "w-tp", approved)
	if err != nil || res != (application.CallbackApply{Result: application.CallbackDuplicate}) {
		t.Fatalf("repeated ApplyCallback = %+v, %v; want duplicate without request data", res, err)
	}
}

func TestQueueStats(t *testing.T) {
	pool := pgtest.NewPool(t)
	q := postgres.NewQueue(pool)
	empty, err := q.Stats(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Counts) != 0 || empty.OldestAge != 0 {
		t.Fatalf("empty queue stats = %+v", empty)
	}

	seed(t, pool, "eastbank", "northbank", "westbank")
	later := seed(t, pool, "eastbank")
	done := seed(t, pool, "northbank")
	exec(t, pool, `UPDATE bank_requests SET status = 'sending', locked_until = now() + interval '1 minute' WHERE bank_code = 'westbank'`)
	exec(t, pool, `UPDATE bank_requests SET next_attempt_at = now() + interval '1 hour' WHERE application_id = $1`, later.ID)
	exec(t, pool, `UPDATE bank_requests SET status = 'approved' WHERE application_id = $1`, done.ID)
	exec(t, pool, `UPDATE bank_requests SET next_attempt_at = now() - interval '30 seconds' WHERE bank_code = 'northbank' AND status = 'queued'`)

	s, err := q.Stats(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := []telemetry.QueueCount{
		{Bank: "eastbank", Status: "queued", Count: 2},
		{Bank: "northbank", Status: "queued", Count: 1},
		{Bank: "westbank", Status: "sending", Count: 1},
	}
	if !reflect.DeepEqual(s.Counts, want) {
		t.Fatalf("counts = %+v, want %+v", s.Counts, want)
	}
	// Самая старая готовая — northbank (30 с назад); eastbank второй заявки в будущем и не считается.
	if s.OldestAge < 30*time.Second || s.OldestAge > 40*time.Second {
		t.Fatalf("oldest age = %s, want about 30s", s.OldestAge)
	}
}
```

- [ ] **Step 3: Запустить — не компилируется**

Run: `go test ./internal/store/postgres/`
Expected: FAIL — `task.TraceParent undefined`, `undefined: application.CallbackApply`, `q.Stats undefined`.

- [ ] **Step 4: Миграция**

Файл: `internal/store/postgres/migrations/00002_trace_parent.sql`
```sql
-- +goose Up
-- Контекст трейса заявки в формате W3C traceparent (раздел 14.4 спеки).
ALTER TABLE bank_requests ADD COLUMN trace_parent text;

-- +goose Down
ALTER TABLE bank_requests DROP COLUMN trace_parent;
```

- [ ] **Step 5: Типы**

В `internal/dispatch/types.go` в `type Task struct` после `Attempts` добавить поле:
```go
	TraceParent string // контекст трейса заявки (W3C traceparent); "" — нет
```

В `internal/application/types.go` после констант `CallbackResult` добавить:
```go
// CallbackApply — итог применения вебхука.
type CallbackApply struct {
	Result      CallbackResult
	RequestID   uuid.UUID // id запроса в банк; только для CallbackApplied
	TraceParent string    // контекст трейса заявки; только для CallbackApplied, "" — не сохранён
}
```

- [ ] **Step 6: Create пишет trace_parent**

В `internal/store/postgres/applications.go` добавить импорт `"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"`. Цикл вставки запросов в `Create` заменить на:
```go
	// Контекст серверного спана POST — родитель всех попыток воркера (раздел 14.4 спеки).
	traceParent := telemetry.TraceParent(ctx)
	for _, code := range bankCodes {
		reqID, err := uuid.NewV7()
		if err != nil {
			return uuid.Nil, nil, fmt.Errorf("generate id: %w", err)
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO bank_requests (id, application_id, bank_code, status, trace_parent)
			VALUES ($1, $2, $3, 'queued', NULLIF($4, ''))`, reqID, app.ID, code, traceParent)
		if err != nil {
			return uuid.Nil, nil, fmt.Errorf("insert bank request: %w", err)
		}
	}
```

- [ ] **Step 7: Claim, ApplyCallback и Stats**

В `internal/store/postgres/queue.go`:

1. Импорты: добавить `"github.com/google/uuid"` и `"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"`.
2. В `claimSQL` строку `RETURNING br.id, br.bank_code, br.attempts,` заменить на `RETURNING br.id, br.bank_code, br.attempts, COALESCE(br.trace_parent, ''),`.
3. В `Claim` вызов `rows.Scan(&t.RequestID, &t.BankCode, &t.Attempts,` заменить на `rows.Scan(&t.RequestID, &t.BankCode, &t.Attempts, &t.TraceParent,`.
4. `ApplyCallback` заменить целиком:
```go
// ApplyCallback записывает решение из вебхука, если запрос ещё ждёт его, и возвращает id и
// trace_parent применённого запроса. Обновление и проверка существования — один SQL-оператор:
// оба видят один снимок данных. Иначе вебхук, пришедший раньше сохранения external_id, мог бы
// получить Duplicate вместо Unknown, и банк не повторил бы его.
func (q *Queue) ApplyCallback(ctx context.Context, bankCode, externalID string, d application.Decision) (application.CallbackApply, error) {
	const (
		guard = ` WHERE bank_code = $1 AND external_id = $2 AND status = 'waiting_callback'`
		tail  = ` RETURNING id, trace_parent)
		SELECT (SELECT id FROM upd), COALESCE((SELECT trace_parent FROM upd), ''),
		       EXISTS (SELECT 1 FROM bank_requests WHERE bank_code = $1 AND external_id = $2)`
	)
	query := `WITH upd AS (UPDATE bank_requests SET status = 'rejected', reject_reason = $3, updated_at = now()` + guard + tail
	args := []any{bankCode, externalID, d.RejectReason}
	if d.Approved {
		query = `WITH upd AS (UPDATE bank_requests SET status = 'approved', approved_amount = $3, rate_bps = $4,
			approved_term = $5, monthly_payment = $6, updated_at = now()` + guard + tail
		args = []any{bankCode, externalID, d.Amount, d.RateBPS, d.TermMonths, d.MonthlyPayment}
	}
	var (
		id          *uuid.UUID // NULL — ничего не обновлено
		traceParent string
		exists      bool
	)
	if err := q.pool.QueryRow(ctx, query, args...).Scan(&id, &traceParent, &exists); err != nil {
		return application.CallbackApply{}, fmt.Errorf("apply callback: %w", err)
	}
	switch {
	case id != nil:
		return application.CallbackApply{Result: application.CallbackApplied, RequestID: *id, TraceParent: traceParent}, nil
	case exists:
		return application.CallbackApply{Result: application.CallbackDuplicate}, nil
	default:
		return application.CallbackApply{Result: application.CallbackUnknown}, nil
	}
}
```
5. В конец файла добавить:
```go
// statsCountsSQL считает незавершённые запросы по банкам. Три ветки вместо status IN (...):
// каждая совпадает с предикатом своего частичного индекса (раздел 7 спеки).
const statsCountsSQL = `
SELECT bank_code, 'queued', count(*) FROM bank_requests WHERE status = 'queued' GROUP BY bank_code
UNION ALL
SELECT bank_code, 'sending', count(*) FROM bank_requests WHERE status = 'sending' GROUP BY bank_code
UNION ALL
SELECT bank_code, 'waiting_callback', count(*) FROM bank_requests WHERE status = 'waiting_callback' GROUP BY bank_code
ORDER BY 1, 2`

// Stats возвращает состояние незавершённых запросов для gauge-метрик (раздел 14.2 спеки).
func (q *Queue) Stats(ctx context.Context) (telemetry.QueueStats, error) {
	var s telemetry.QueueStats
	rows, err := q.pool.Query(ctx, statsCountsSQL)
	if err != nil {
		return s, fmt.Errorf("queue counts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c telemetry.QueueCount
		if err := rows.Scan(&c.Bank, &c.Status, &c.Count); err != nil {
			return s, fmt.Errorf("scan queue count: %w", err)
		}
		s.Counts = append(s.Counts, c)
	}
	if err := rows.Err(); err != nil {
		return s, fmt.Errorf("read queue counts: %w", err)
	}
	var seconds float64
	err = q.pool.QueryRow(ctx, `
		SELECT COALESCE(EXTRACT(EPOCH FROM now() - min(next_attempt_at)), 0)::float8
		FROM bank_requests WHERE status = 'queued' AND next_attempt_at <= now()`).Scan(&seconds)
	if err != nil {
		return s, fmt.Errorf("oldest queued age: %w", err)
	}
	s.OldestAge = time.Duration(seconds * float64(time.Second))
	return s, nil
}
```

- [ ] **Step 8: Обновить существующие тесты вебхуков в хранилище**

В `internal/store/postgres/queue_test.go`:
- в `TestCompleteAcceptedThenCallback` в помощнике `apply` строку `return res` заменить на `return res.Result`;
- в `TestCallbackWithRejection` условие `if err != nil || res != application.CallbackApplied {` заменить на `if err != nil || res.Result != application.CallbackApplied {`;
- в `TestCallbackBeforeExternalIDIsUnknown` условие `if err != nil || res != application.CallbackUnknown {` заменить на `if err != nil || res.Result != application.CallbackUnknown {`.

- [ ] **Step 9: Подстроить httpapi под новый результат ApplyCallback**

В `internal/httpapi/server.go` в интерфейсе `CallbackStore` сигнатуру заменить на:
```go
	ApplyCallback(ctx context.Context, bankCode, externalID string, d application.Decision) (application.CallbackApply, error)
```

В `internal/httpapi/webhooks.go` строку `switch res {` заменить на `switch res.Result {`.

В `internal/httpapi/webhooks_test.go` метод фейка заменить на:
```go
func (f *fakeCallbacks) ApplyCallback(_ context.Context, bankCode, externalID string, d application.Decision) (application.CallbackApply, error) {
	f.calls = append(f.calls, callbackCall{bankCode: bankCode, externalID: externalID, decision: d})
	return application.CallbackApply{Result: f.result}, f.err
}
```

- [ ] **Step 10: Запустить тесты**

Run: `go test -race ./internal/store/postgres/ ./internal/httpapi/ ./internal/dispatch/ ./test/e2e/`
Expected: PASS — новые тесты хранилища зелёные, прежние тесты httpapi, воркера и e2e не сломаны.

- [ ] **Step 11: Проверка и commit**

```bash
make lint && make test
git add internal/store/postgres internal/dispatch/types.go internal/application/types.go internal/httpapi
git commit -m "feat(store): keep trace context in bank requests and report queue stats"
```

---

### Task 3: Метрики и трейсинг HTTP API

**Ветка:** `feature/http-telemetry` · **Этап 2, облако, после merge Task 2** · **Спека:** 14.2, 14.4, 14.5

**Files:**
- Create: `internal/httpapi/instrument.go`, `internal/httpapi/telemetry_test.go`
- Modify: `internal/httpapi/server.go`, `internal/httpapi/applications.go`, `internal/httpapi/webhooks.go`, `internal/httpapi/webhooks_test.go`, `internal/application/types.go`, `internal/application/service.go`, `internal/application/service_test.go`

**Interfaces:**
- Consumes: `*telemetry.Metrics` и методы `HTTPRequest`, `Application`, `Webhook`, `Decision`; константы меток; `telemetry.WithTraceParent`; `telemetrytest.Tracer`, `Value`, `Spans`, `Attr` (Task 1). `application.CallbackApply` (Task 2).
- Produces:
  - `httpapi.Deps.Metrics *telemetry.Metrics`, `httpapi.Deps.Tracer trace.Tracer` — оба необязательны
  - `application.View.Replayed bool` — заполняет `Service.Create`

- [ ] **Step 1: Создать ветку**

```bash
git switch develop && git pull --ff-only && git switch -c feature/http-telemetry
```

- [ ] **Step 2: Тест на признак повтора в сервисе**

В `internal/application/service_test.go` в конец `TestServiceCreateIsIdempotent` добавить:
```go
	if first.Replayed || !second.Replayed {
		t.Fatalf("Replayed: first %v, second %v; want false then true", first.Replayed, second.Replayed)
	}
```

Run: `go test ./internal/application/ -run TestServiceCreateIsIdempotent`
Expected: FAIL — `first.Replayed undefined`.

- [ ] **Step 3: Признак повтора**

В `internal/application/types.go` в `type View struct` после `Banks` добавить:
```go
	// Replayed заполняет только Service.Create: заявка уже существовала, вызов — повтор
	// с тем же ключом идемпотентности и тем же телом.
	Replayed bool
```

В `internal/application/service.go` в `Create` последнюю строку `return s.repo.Get(ctx, id)` заменить на:
```go
	v, err := s.repo.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	v.Replayed = id != app.ID
	return v, nil
```

Run: `go test ./internal/application/`
Expected: PASS.

- [ ] **Step 4: Расширить фейк вебхуков**

В `internal/httpapi/webhooks_test.go` добавить импорт `"github.com/google/uuid"`, а фейк заменить на:
```go
type fakeCallbacks struct {
	result      application.CallbackResult
	requestID   uuid.UUID
	traceParent string
	err         error
	calls       []callbackCall
}

func (f *fakeCallbacks) ApplyCallback(_ context.Context, bankCode, externalID string, d application.Decision) (application.CallbackApply, error) {
	f.calls = append(f.calls, callbackCall{bankCode: bankCode, externalID: externalID, decision: d})
	return application.CallbackApply{Result: f.result, RequestID: f.requestID, TraceParent: f.traceParent}, f.err
}
```

- [ ] **Step 5: Написать падающие тесты метрик и спанов API**

Файл: `internal/httpapi/telemetry_test.go`
```go
package httpapi

import (
	"context"
	"errors"
	"net/http"
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
	svc.get = func(context.Context, uuid.UUID) (application.View, error) { return application.View{}, application.ErrNotFound }
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
```

- [ ] **Step 6: Запустить — не компилируется**

Run: `go test ./internal/httpapi/`
Expected: FAIL — `unknown field Metrics in struct literal of type Deps`.

- [ ] **Step 7: Обёртка маршрутов**

Файл: `internal/httpapi/instrument.go`
```go
package httpapi

import (
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"
)

// untracedRoutes — служебные маршруты: учитываются в метриках, но спанов не создают (раздел 14.4 спеки).
var untracedRoutes = map[string]bool{"/healthz": true, "/readyz": true}

// knownMethods — значения метки method; прочие методы считаются как other, иначе сканер плодил бы серии.
var knownMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true, http.MethodOptions: true,
}

// instrument оборачивает обработчик маршрута route серверным спаном и метриками HTTP (раздел 14 спеки).
// route — шаблон из таблицы маршрутов или telemetry.Other, а не сырой путь.
func (a *api) instrument(route string, next http.HandlerFunc) http.HandlerFunc {
	traced := !untracedRoutes[route]
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		method := r.Method
		if !knownMethods[method] {
			method = telemetry.Other
		}
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		if traced {
			ctx := propagation.TraceContext{}.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
			ctx, span := a.Tracer.Start(ctx, method+" "+route,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(attribute.String("http.request.method", method), attribute.String("http.route", route)))
			defer func() {
				span.SetAttributes(attribute.Int("http.response.status_code", rec.code))
				if rec.code >= http.StatusInternalServerError {
					span.SetStatus(codes.Error, http.StatusText(rec.code))
				}
				span.End()
			}()
			r = r.WithContext(ctx)
		}
		next(rec, r)
		a.Metrics.HTTPRequest(route, method, rec.code, time.Since(start))
	}
}

// statusRecorder запоминает код ответа для метрик и спана.
type statusRecorder struct {
	http.ResponseWriter
	code        int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.code, s.wroteHeader = code, true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wroteHeader = true
	return s.ResponseWriter.Write(b)
}

// Unwrap даёт http.ResponseController доступ к исходному ResponseWriter.
func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}
```

- [ ] **Step 8: Подключить обёртку и зависимости**

В `internal/httpapi/server.go`:
1. Импорты: добавить `"go.opentelemetry.io/otel/trace"`, `"go.opentelemetry.io/otel/trace/noop"` и `"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"`.
2. В `type Deps struct` после `Now` добавить:
```go
	Metrics        *telemetry.Metrics // nil — метрики не пишутся
	Tracer         trace.Tracer       // nil — спаны не создаются
```
3. В `NewHandler` после `if d.Now == nil { ... }` добавить:
```go
	if d.Tracer == nil {
		d.Tracer = noop.NewTracerProvider().Tracer("")
	}
```
4. В `NewHandler` регистрацию маршрутов заменить на:
```go
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
```

- [ ] **Step 9: Метрики и атрибуты в обработчиках заявок**

В `internal/httpapi/applications.go`:
1. Импорты: добавить `"go.opentelemetry.io/otel/attribute"`, `"go.opentelemetry.io/otel/trace"` и `"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"`.
2. В `createApplication` блок `switch` после вызова `Create` заменить на:
```go
	switch {
	case errors.As(err, &verr):
		writeProblem(w, http.StatusBadRequest, "Validation failed", "request body has invalid fields", verr.Fields...)
	case errors.Is(err, application.ErrIdempotencyConflict):
		a.Metrics.Application(telemetry.ApplicationConflict)
		writeProblem(w, http.StatusConflict, "Idempotency key conflict", err.Error())
	case err != nil:
		a.Log.ErrorContext(r.Context(), "create application failed", "error", err)
		writeProblem(w, http.StatusInternalServerError, "Internal error", "")
	default:
		result := telemetry.ApplicationCreated
		if v.Replayed {
			result = telemetry.ApplicationReplayed
		}
		a.Metrics.Application(result)
		trace.SpanFromContext(r.Context()).SetAttributes(attribute.String("application.id", v.ID.String()))
		writeJSON(w, http.StatusAccepted, createResponse{ID: v.ID, Status: v.Status()})
	}
```
3. В `getApplication` сразу после успешного `uuid.Parse` (после блока `if err != nil { ... return }`) добавить:
```go
	trace.SpanFromContext(r.Context()).SetAttributes(attribute.String("application.id", id.String()))
```
и строку `a.Log.Error("get application failed", "application_id", id, "error", err)` заменить на `a.Log.ErrorContext(r.Context(), "get application failed", "application_id", id, "error", err)`.

- [ ] **Step 10: Вебхуки — метрики и спан в трейсе заявки**

Файл `internal/httpapi/webhooks.go` заменить целиком:
```go
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"
)

type webhookResponse struct {
	Result string `json:"result"`
}

// bankWebhook — POST /v1/webhooks/banks/{code}: асинхронное решение банка (раздел 5.3 спеки).
func (a *api) bankWebhook(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	wb, ok := a.WebhookBanks[code]
	if !ok {
		// Код из пути в метку не идёт: произвольные коды плодили бы серии.
		a.Metrics.Webhook(telemetry.Other, telemetry.WebhookUnknownBank)
		writeProblem(w, http.StatusNotFound, "Unknown bank", fmt.Sprintf("bank %q does not accept webhooks", code))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		a.Metrics.Webhook(code, telemetry.WebhookBadBody)
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeProblem(w, http.StatusRequestEntityTooLarge, "Body too large", fmt.Sprintf("body exceeds %d bytes", maxBodyBytes))
			return
		}
		writeProblem(w, http.StatusBadRequest, "Invalid request body", "cannot read body")
		return
	}
	ctx := r.Context()
	log := a.Log.With("bank_code", code)
	ts, sig := r.Header.Get(bank.HeaderWebhookTimestamp), r.Header.Get(bank.HeaderWebhookSignature)
	if err := bank.VerifyWebhook(wb.Secret, ts, sig, body, a.Now(), a.WebhookMaxSkew); err != nil {
		a.Metrics.Webhook(code, telemetry.WebhookUnauthorized)
		log.WarnContext(ctx, "webhook rejected", "error", err)
		writeProblem(w, http.StatusUnauthorized, "Invalid signature", "webhook signature is missing, invalid or expired")
		return
	}
	externalID, decision, err := wb.Handler.ParseCallback(body)
	if err != nil {
		a.Metrics.Webhook(code, telemetry.WebhookBadBody)
		log.WarnContext(ctx, "webhook body rejected", "error", err)
		writeProblem(w, http.StatusBadRequest, "Invalid callback", err.Error())
		return
	}
	log = log.With("external_id", externalID)
	start := time.Now()
	res, err := a.Callbacks.ApplyCallback(ctx, code, externalID, decision)
	if err != nil {
		a.Metrics.Webhook(code, telemetry.WebhookError)
		log.ErrorContext(ctx, "apply webhook failed", "error", err)
		writeProblem(w, http.StatusInternalServerError, "Internal error", "")
		return
	}
	switch res.Result {
	case application.CallbackApplied:
		a.Metrics.Webhook(code, telemetry.WebhookApplied)
		a.Metrics.Decision(code, decision.Approved, telemetry.ViaWebhook)
		a.traceCallback(ctx, code, res, decision, start)
		log.InfoContext(ctx, "webhook applied")
		writeJSON(w, http.StatusOK, webhookResponse{Result: "applied"})
	case application.CallbackDuplicate:
		a.Metrics.Webhook(code, telemetry.WebhookDuplicate)
		log.WarnContext(ctx, "webhook ignored: request is not waiting for a callback")
		writeJSON(w, http.StatusOK, webhookResponse{Result: "ignored"})
	default:
		a.Metrics.Webhook(code, telemetry.WebhookUnknown)
		log.WarnContext(ctx, "webhook for unknown request")
		writeProblem(w, http.StatusNotFound, "Unknown request", "no request with this external id")
	}
}

// traceCallback добавляет в трейс заявки спан применённого вебхука (раздел 14.4 спеки).
// Банк наш контекст не присылает, поэтому родитель — trace_parent запроса, а со спаном самого
// вебхука новый спан связан ссылкой.
func (a *api) traceCallback(ctx context.Context, code string, res application.CallbackApply, d application.Decision, start time.Time) {
	if res.TraceParent == "" {
		return
	}
	status := application.RequestRejected
	if d.Approved {
		status = application.RequestApproved
	}
	_, span := a.Tracer.Start(telemetry.WithTraceParent(ctx, res.TraceParent), "bank_request.callback",
		trace.WithTimestamp(start),
		trace.WithLinks(trace.Link{SpanContext: trace.SpanContextFromContext(ctx)}),
		trace.WithAttributes(
			attribute.String("bank_request.id", res.RequestID.String()),
			attribute.String("bank.code", code),
			attribute.String("decision.status", string(status)),
		))
	span.End()
}
```

- [ ] **Step 11: Запустить тесты**

Run: `go test -race ./internal/httpapi/ ./internal/application/`
Expected: PASS — новые тесты и все прежние тесты API.

- [ ] **Step 12: Проверка и commit**

```bash
make lint && make test
git add internal/httpapi internal/application
git commit -m "feat(httpapi): add HTTP, application and webhook metrics and server spans"
```

---

### Task 4: Метрики и трейсинг воркера

**Ветка:** `feature/worker-telemetry` · **Этап 2, облако, после merge Task 2, параллельно с Task 3 и Task 5** · **Спека:** 14.2, 14.4, 14.5

**Files:**
- Modify: `internal/dispatch/worker.go`, `internal/dispatch/worker_test.go`
- Create: `internal/dispatch/telemetry_test.go`

**Interfaces:**
- Consumes: `*telemetry.Metrics` и методы `BankCall`, `BankCallDuration`, `Decision`, `TaskStarted`, `TaskFinished`, `QueueError`, `LeaseLost`, `Swept`; константы меток; `telemetry.WithTraceParent`; `telemetrytest.*` (Task 1). `Task.TraceParent` (Task 2).
- Produces: `dispatch.Config.Metrics *telemetry.Metrics`, `dispatch.Config.Tracer trace.Tracer` — оба необязательны

- [ ] **Step 1: Создать ветку**

```bash
git switch develop && git pull --ff-only && git switch -c feature/worker-telemetry
```

- [ ] **Step 2: Расширить фейк очереди**

В `internal/dispatch/worker_test.go` в `type fakeQueue struct` после поля `hangComplete` добавить:
```go
	lostLease    bool       // Complete сообщает, что аренду перехватили
	completeErr  error      // Complete возвращает эту ошибку
	sweepStats   SweepStats // что возвращает Sweep
```
В методе `Complete` после проверки `if err := ctx.Err(); err != nil { ... }` добавить:
```go
	if q.completeErr != nil {
		return false, q.completeErr
	}
	if q.lostLease {
		return false, nil
	}
```
В методе `Sweep` строку `return SweepStats{}, nil` (последнюю) заменить на `return q.sweepStats, nil`.

- [ ] **Step 3: Написать падающие тесты**

Файл: `internal/dispatch/telemetry_test.go`
```go
package dispatch

import (
	"context"
	"errors"
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
	if telemetrytest.Has(t, reg, "loangw_bank_decisions_total") {
		t.Error("decision is counted although it was not saved")
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
```

- [ ] **Step 4: Запустить — не компилируется**

Run: `go test ./internal/dispatch/`
Expected: FAIL — `cfg.Metrics undefined (type Config has no field or method Metrics)`.

- [ ] **Step 5: Встроить метрики и спаны в воркер**

В `internal/dispatch/worker.go`:

1. Импорты заменить на:
```go
import (
	"context"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"
)
```
2. В `type Config struct` после `DBTimeout` добавить:
```go
	Metrics         *telemetry.Metrics       // nil — метрики не пишутся
	Tracer          trace.Tracer             // nil — спаны не создаются
```
3. `type Worker` и `New` заменить на:
```go
// Worker забирает задачи из очереди, вызывает банки и записывает результаты.
type Worker struct {
	queue  Queue
	banks  *bank.Registry
	cfg    Config
	log    *slog.Logger
	rnd    func(int64) int64
	tracer trace.Tracer
}

// New создаёт воркер.
func New(queue Queue, banks *bank.Registry, cfg Config, log *slog.Logger) *Worker {
	tracer := cfg.Tracer
	if tracer == nil {
		tracer = noop.NewTracerProvider().Tracer("")
	}
	return &Worker{queue: queue, banks: banks, cfg: cfg, log: log, rnd: rand.Int64N, tracer: tracer}
}
```
4. В `poll` блок обработки ошибки `Claim` заменить на:
```go
	if err != nil {
		if ctx.Err() == nil {
			w.cfg.Metrics.QueueError(telemetry.OpClaim)
			w.log.Error("claim failed", "error", err)
		}
		return
	}
```
5. `sweep` заменить на:
```go
func (w *Worker) sweep(ctx context.Context) {
	sweepCtx, cancel := context.WithTimeout(ctx, w.dbTimeout())
	defer cancel()
	s, err := w.queue.Sweep(sweepCtx, w.cfg.MaxAttempts)
	if err != nil {
		if ctx.Err() == nil {
			w.cfg.Metrics.QueueError(telemetry.OpSweep)
			w.log.Error("sweep failed", "error", err)
		}
		return
	}
	w.cfg.Metrics.Swept(s.CallbackTimeouts, s.Requeued, s.LeaseExpired)
	if s != (SweepStats{}) {
		w.log.Info("sweep", "callback_timeouts", s.CallbackTimeouts, "requeued", s.Requeued, "lease_expired", s.LeaseExpired)
	}
}
```
6. `process` и `call` заменить на:
```go
// process вызывает банк и записывает результат. В лог и спаны попадают только идентификаторы — без ПДн.
func (w *Worker) process(ctx context.Context, t Task) {
	w.cfg.Metrics.TaskStarted()
	defer w.cfg.Metrics.TaskFinished()
	// Родитель — серверный спан POST, создавший заявку: так все попытки видны в трейсе заявки.
	ctx, span := w.tracer.Start(telemetry.WithTraceParent(ctx, t.TraceParent), "bank_request.process",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("application.id", t.Application.ID.String()),
			attribute.String("bank_request.id", t.RequestID.String()),
			attribute.String("bank.code", t.BankCode),
			attribute.Int("attempt", t.Attempts),
		))
	defer span.End()
	log := w.log.With("bank_request_id", t.RequestID, "application_id", t.Application.ID,
		"bank_code", t.BankCode, "attempt", t.Attempts)

	out := w.call(ctx, t)
	span.SetAttributes(attribute.String("outcome", out.Kind.String()))
	w.cfg.Metrics.BankCall(t.BankCode, out.Kind.String())

	ok, err := w.save(ctx, t, out)
	switch {
	case err != nil:
		span.SetStatus(codes.Error, err.Error())
		w.cfg.Metrics.QueueError(telemetry.OpComplete)
		log.ErrorContext(ctx, "save bank result failed", "outcome", out.Kind.String(), "error", err)
	case !ok:
		w.cfg.Metrics.LeaseLost()
		log.WarnContext(ctx, "lease lost, bank result dropped", "outcome", out.Kind.String())
	case out.Kind == OutcomeRetry || out.Kind == OutcomeFailed:
		span.SetStatus(codes.Error, out.Error)
		log.WarnContext(ctx, "bank request failed", "outcome", out.Kind.String(), "error", out.Error, "retry_after", out.RetryAfter.String())
	default:
		if out.Kind == OutcomeDecision {
			w.cfg.Metrics.Decision(t.BankCode, out.Decision.Approved, telemetry.ViaSync)
		}
		log.InfoContext(ctx, "bank request processed", "outcome", out.Kind.String())
	}
}

// save записывает результат в очередь под спаном queue.complete.
func (w *Worker) save(ctx context.Context, t Task, out Outcome) (bool, error) {
	// Запись не зависит от отмены задач: иначе ответ банка, полученный к ShutdownTimeout,
	// пропадёт, а задача провисит в sending до конца аренды. Таймаут — на случай зависшей БД.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.dbTimeout())
	defer cancel()
	saveCtx, span := w.tracer.Start(saveCtx, "queue.complete")
	defer span.End()
	ok, err := w.queue.Complete(saveCtx, t, out)
	result := "saved"
	switch {
	case err != nil:
		result = "error"
		span.SetStatus(codes.Error, err.Error())
	case !ok:
		result = "lease_lost"
	}
	span.SetAttributes(attribute.String("result", result))
	return ok, err
}

// call вызывает банк с таймаутом под спаном bank.submit и превращает ответ в Outcome.
func (w *Worker) call(ctx context.Context, t Task) Outcome {
	b, ok := w.banks.Get(t.BankCode)
	if !ok {
		return Outcome{Kind: OutcomeFailed, Error: "unknown bank"}
	}
	callCtx, cancel := context.WithTimeout(ctx, w.bankTimeout(t.BankCode))
	defer cancel()
	callCtx, span := w.tracer.Start(callCtx, "bank.submit", trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("bank.code", t.BankCode)))
	defer span.End()
	start := time.Now()
	res, err := b.Submit(callCtx, bank.SubmitRequest{RequestID: t.RequestID, Application: t.Application})
	out := w.outcome(t, res, err)
	w.cfg.Metrics.BankCallDuration(t.BankCode, out.Kind.String(), time.Since(start))
	span.SetAttributes(attribute.String("outcome", out.Kind.String()))
	if err != nil {
		errType := "temporary" // ошибка без класса тоже временная (раздел 6.2 спеки)
		if bank.IsPermanent(err) {
			errType = "permanent"
		}
		span.SetAttributes(attribute.String("error.type", errType))
		span.SetStatus(codes.Error, err.Error())
	}
	return out
}

// outcome превращает ответ банка в Outcome по разделу 8 спеки.
func (w *Worker) outcome(t Task, res bank.SubmitResult, err error) Outcome {
	if err != nil {
		if bank.IsPermanent(err) || t.Attempts >= w.cfg.MaxAttempts {
			return Outcome{Kind: OutcomeFailed, Error: err.Error()}
		}
		return Outcome{
			Kind:       OutcomeRetry,
			Error:      err.Error(),
			RetryAfter: Backoff(t.Attempts, w.cfg.BaseDelay, w.cfg.MaxDelay, w.rnd),
		}
	}
	if res.Decision != nil {
		return Outcome{Kind: OutcomeDecision, Decision: *res.Decision}
	}
	if res.ExternalID == "" {
		return Outcome{Kind: OutcomeFailed, Error: "bank returned neither decision nor external id"}
	}
	return Outcome{Kind: OutcomeAccepted, ExternalID: res.ExternalID, CallbackTimeout: w.cfg.CallbackTimeout}
}
```

- [ ] **Step 6: Запустить тесты**

Run: `go test -race -count=3 ./internal/dispatch/`
Expected: PASS — новые тесты и все прежние (`TestCallOutcomes`, остановка, лимит слотов, зависшая БД).

- [ ] **Step 7: Проверка и commit**

```bash
make lint && make test
git add internal/dispatch
git commit -m "feat(dispatch): add worker metrics and bank request spans"
```

---

### Task 5: Prometheus, алерты, Grafana и Jaeger в compose

**Ветка:** `feature/observability-stack` · **Этап 2, облако, параллельно с Task 3 и Task 4** · **Спека:** 12, 14.2, 14.3

**Files:**
- Create: `deploy/observability/prometheus.yml`, `deploy/observability/alerts.yml`, `deploy/observability/alerts_test.yml`, `deploy/observability/grafana/provisioning/datasources/datasources.yml`, `deploy/observability/grafana/provisioning/dashboards/dashboards.yml`, `deploy/observability/grafana/dashboards/loan-gateway.json`
- Modify: `compose.yaml`, `scripts/demo.sh`, `Makefile`, `.github/workflows/ci.yml`, `CLAUDE.md`

**Interfaces:**
- Consumes: имена и метки метрик — таблица 14.2 спеки (код Task 1–4 не нужен: конфиги ссылаются только на имена).
- Produces: `make obs-check`; сервисы compose `prometheus` (9090), `jaeger` (16686), `grafana` (3000); у `api` и `worker` переменные `OTEL_EXPORTER_OTLP_ENDPOINT` и `OTEL_SERVICE_NAME`; ссылки в `make demo`.

- [ ] **Step 1: Создать ветку**

```bash
git switch develop && git pull --ff-only && git switch -c feature/observability-stack
```

- [ ] **Step 2: Написать тесты алертов (сначала тест)**

Файл: `deploy/observability/alerts_test.yml`
```yaml
# Юнит-тесты правил алертов: make obs-check (promtool test rules).
rule_files:
  - alerts.yml

evaluation_interval: 1m

tests:
  - interval: 1m
    input_series:
      - series: 'loangw_bank_calls_total{bank="eastbank",outcome="retry"}'
        values: '0+2x15'
      - series: 'loangw_bank_calls_total{bank="eastbank",outcome="decision"}'
        values: '0+1x15'
      - series: 'loangw_bank_calls_total{bank="northbank",outcome="decision"}'
        values: '0+3x15'
    alert_rule_test:
      - eval_time: 10m
        alertname: BankErrorRateHigh
        exp_alerts:
          - exp_labels:
              severity: warning
              bank: eastbank
            exp_annotations:
              summary: "У eastbank больше 20 % вызовов заканчиваются ретраем или ошибкой"

  - interval: 1m
    input_series:
      - series: 'up{job="worker",instance="worker:9100"}'
        values: '1 1 0 0 0'
    alert_rule_test:
      - eval_time: 2m
        alertname: TargetDown
        exp_alerts: []
      - eval_time: 4m
        alertname: TargetDown
        exp_alerts:
          - exp_labels:
              severity: critical
              job: worker
              instance: "worker:9100"
            exp_annotations:
              summary: "worker не отвечает на опрос Prometheus"

  - interval: 1m
    input_series:
      - series: 'loangw_queue_oldest_age_seconds{job="worker",instance="worker:9100"}'
        values: '0 30 70 80 90 100'
    alert_rule_test:
      - eval_time: 5m
        alertname: QueueBacklog
        exp_alerts:
          - exp_labels:
              severity: warning
            exp_annotations:
              summary: "Готовая задача ждёт отправки дольше минуты"

  - interval: 1m
    input_series:
      - series: 'loangw_sweeper_actions_total{action="callback_timeout",job="worker",instance="worker:9100"}'
        values: '0 0 1 1'
    alert_rule_test:
      - eval_time: 3m
        alertname: CallbackTimeouts
        exp_alerts:
          - exp_labels:
              severity: warning
              action: callback_timeout
              job: worker
              instance: "worker:9100"
            exp_annotations:
              summary: "Банк не прислал вебхук к дедлайну"
```

- [ ] **Step 3: Добавить цель obs-check и убедиться, что тест падает**

В `Makefile`:
1. В начало добавить переменную рядом с `GOLANGCI_LINT`:
```makefile
PROMETHEUS_IMAGE = prom/prometheus:v3.15.0
```
2. В `.PHONY` добавить `obs-check`.
3. В конец добавить:
```makefile
## obs-check: конфиг Prometheus, тесты алертов и JSON дашборда (нужен Docker)
obs-check:
	docker run --rm -v "$(CURDIR)/deploy/observability:/etc/prometheus:ro" --entrypoint promtool $(PROMETHEUS_IMAGE) check config /etc/prometheus/prometheus.yml
	docker run --rm -v "$(CURDIR)/deploy/observability:/w:ro" -w /w --entrypoint promtool $(PROMETHEUS_IMAGE) test rules alerts_test.yml
	python3 -m json.tool deploy/observability/grafana/dashboards/loan-gateway.json > /dev/null
```

Run: `make obs-check`
Expected: FAIL — `prometheus.yml` и `alerts.yml` не существуют.

- [ ] **Step 4: Конфиг Prometheus и правила алертов**

Файл: `deploy/observability/prometheus.yml`
```yaml
# Prometheus локального стенда: опрос api и worker, правила алертов (раздел 14.3 спеки).
global:
  scrape_interval: 5s
  evaluation_interval: 5s

rule_files:
  - /etc/prometheus/alerts.yml

scrape_configs:
  - job_name: api
    static_configs:
      - targets: ["api:9100"]
  - job_name: worker
    static_configs:
      - targets: ["worker:9100"]
```

Файл: `deploy/observability/alerts.yml`
```yaml
# Алерты шлюза (раздел 14.3 спеки). Alertmanager не входит: срабатывания видны в UI Prometheus и Grafana.
groups:
  - name: loan-gateway
    rules:
      - alert: TargetDown
        expr: up{job=~"api|worker"} == 0
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "{{ $labels.job }} не отвечает на опрос Prometheus"

      - alert: BankErrorRateHigh
        expr: |
          sum by (bank) (rate(loangw_bank_calls_total{outcome=~"retry|failed"}[5m]))
            / sum by (bank) (rate(loangw_bank_calls_total[5m])) > 0.2
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "У {{ $labels.bank }} больше 20 % вызовов заканчиваются ретраем или ошибкой"

      - alert: QueueBacklog
        expr: max(loangw_queue_oldest_age_seconds) > 60
        for: 2m
        labels:
          severity: warning
        annotations:
          summary: "Готовая задача ждёт отправки дольше минуты"

      - alert: CallbackTimeouts
        expr: increase(loangw_sweeper_actions_total{action="callback_timeout"}[10m]) > 0
        labels:
          severity: warning
        annotations:
          summary: "Банк не прислал вебхук к дедлайну"
```

- [ ] **Step 5: Grafana — источники данных и дашборд**

Файл: `deploy/observability/grafana/provisioning/datasources/datasources.yml`
```yaml
apiVersion: 1
datasources:
  - name: Prometheus
    type: prometheus
    uid: prometheus
    access: proxy
    url: http://prometheus:9090
    isDefault: true
  - name: Jaeger
    type: jaeger
    uid: jaeger
    access: proxy
    url: http://jaeger:16686
```

Файл: `deploy/observability/grafana/provisioning/dashboards/dashboards.yml`
```yaml
apiVersion: 1
providers:
  - name: loan-gateway
    type: file
    options:
      path: /var/lib/grafana/dashboards
```

Файл: `deploy/observability/grafana/dashboards/loan-gateway.json`
```json
{
  "uid": "loan-gateway",
  "title": "Loan Gateway",
  "schemaVersion": 39,
  "refresh": "10s",
  "time": { "from": "now-30m", "to": "now" },
  "panels": [
    {
      "id": 1, "type": "timeseries", "title": "API: запросы в секунду по маршрутам и кодам",
      "gridPos": { "h": 8, "w": 12, "x": 0, "y": 0 },
      "datasource": { "type": "prometheus", "uid": "prometheus" },
      "fieldConfig": { "defaults": { "unit": "reqps" }, "overrides": [] },
      "targets": [
        { "refId": "A", "expr": "sum by (route, code) (rate(loangw_http_requests_total[1m]))", "legendFormat": "{{route}} {{code}}" }
      ]
    },
    {
      "id": 2, "type": "timeseries", "title": "Задержка вызовов банков: p50, p95, p99",
      "gridPos": { "h": 8, "w": 12, "x": 12, "y": 0 },
      "datasource": { "type": "prometheus", "uid": "prometheus" },
      "fieldConfig": { "defaults": { "unit": "s" }, "overrides": [] },
      "targets": [
        { "refId": "A", "expr": "histogram_quantile(0.5, sum by (le, bank) (rate(loangw_bank_call_duration_seconds_bucket[5m])))", "legendFormat": "p50 {{bank}}" },
        { "refId": "B", "expr": "histogram_quantile(0.95, sum by (le, bank) (rate(loangw_bank_call_duration_seconds_bucket[5m])))", "legendFormat": "p95 {{bank}}" },
        { "refId": "C", "expr": "histogram_quantile(0.99, sum by (le, bank) (rate(loangw_bank_call_duration_seconds_bucket[5m])))", "legendFormat": "p99 {{bank}}" }
      ]
    },
    {
      "id": 3, "type": "timeseries", "title": "Исходы вызовов банков",
      "gridPos": { "h": 8, "w": 12, "x": 0, "y": 8 },
      "datasource": { "type": "prometheus", "uid": "prometheus" },
      "fieldConfig": { "defaults": { "unit": "ops" }, "overrides": [] },
      "targets": [
        { "refId": "A", "expr": "sum by (bank, outcome) (rate(loangw_bank_calls_total[5m]))", "legendFormat": "{{bank}} {{outcome}}" }
      ]
    },
    {
      "id": 4, "type": "timeseries", "title": "Решения банков",
      "gridPos": { "h": 8, "w": 12, "x": 12, "y": 8 },
      "datasource": { "type": "prometheus", "uid": "prometheus" },
      "fieldConfig": { "defaults": { "unit": "short" }, "overrides": [] },
      "targets": [
        { "refId": "A", "expr": "sum by (bank, status, via) (increase(loangw_bank_decisions_total[5m]))", "legendFormat": "{{bank}} {{status}} ({{via}})" }
      ]
    },
    {
      "id": 5, "type": "timeseries", "title": "Очередь по статусам",
      "gridPos": { "h": 8, "w": 8, "x": 0, "y": 16 },
      "datasource": { "type": "prometheus", "uid": "prometheus" },
      "fieldConfig": { "defaults": { "unit": "short" }, "overrides": [] },
      "targets": [
        { "refId": "A", "expr": "max by (bank, status) (loangw_bank_requests)", "legendFormat": "{{bank}} {{status}}" }
      ]
    },
    {
      "id": 6, "type": "stat", "title": "Возраст самой старой готовой задачи",
      "gridPos": { "h": 4, "w": 8, "x": 8, "y": 16 },
      "datasource": { "type": "prometheus", "uid": "prometheus" },
      "fieldConfig": { "defaults": { "unit": "s" }, "overrides": [] },
      "targets": [
        { "refId": "A", "expr": "max(loangw_queue_oldest_age_seconds)" }
      ]
    },
    {
      "id": 7, "type": "stat", "title": "Вызовы банков в работе",
      "gridPos": { "h": 4, "w": 8, "x": 8, "y": 20 },
      "datasource": { "type": "prometheus", "uid": "prometheus" },
      "fieldConfig": { "defaults": { "unit": "short" }, "overrides": [] },
      "targets": [
        { "refId": "A", "expr": "sum(loangw_worker_tasks_in_flight)" }
      ]
    },
    {
      "id": 8, "type": "timeseries", "title": "Чистильщик и вебхуки",
      "gridPos": { "h": 8, "w": 8, "x": 16, "y": 16 },
      "datasource": { "type": "prometheus", "uid": "prometheus" },
      "fieldConfig": { "defaults": { "unit": "short" }, "overrides": [] },
      "targets": [
        { "refId": "A", "expr": "sum by (action) (increase(loangw_sweeper_actions_total[5m]))", "legendFormat": "чистильщик: {{action}}" },
        { "refId": "B", "expr": "sum by (bank, result) (increase(loangw_webhooks_total[5m]))", "legendFormat": "вебхук {{bank}}: {{result}}" }
      ]
    }
  ]
}
```

- [ ] **Step 6: Запустить проверку конфигов**

Run: `make obs-check`
Expected: PASS — `SUCCESS` от `promtool check config` и `test rules`, JSON корректен.

- [ ] **Step 7: Сервисы в compose**

В `compose.yaml`:
1. В `x-gateway-env` после `BANK_WESTBANK_WEBHOOK_SECRET` добавить:
```yaml
  OTEL_EXPORTER_OTLP_ENDPOINT: http://jaeger:4318
```
2. У сервиса `api` строку `environment: *gateway-env` заменить на:
```yaml
    environment:
      <<: *gateway-env
      OTEL_SERVICE_NAME: gateway-api
```
3. У сервиса `worker` строку `environment: *gateway-env` заменить на:
```yaml
    environment:
      <<: *gateway-env
      OTEL_SERVICE_NAME: gateway-worker
```
4. В конец `services` добавить:
```yaml
  prometheus:
    image: prom/prometheus:v3.15.0
    volumes:
      - ./deploy/observability/prometheus.yml:/etc/prometheus/prometheus.yml:ro
      - ./deploy/observability/alerts.yml:/etc/prometheus/alerts.yml:ro
    ports:
      - "9090:9090"

  jaeger:
    image: jaegertracing/jaeger:2.21.0
    ports:
      - "16686:16686" # UI; приём OTLP (4318) доступен только внутри сети compose

  grafana:
    image: grafana/grafana:13.2.3
    environment:
      # Анонимный вход с правами администратора — только для локального стенда.
      GF_AUTH_ANONYMOUS_ENABLED: "true"
      GF_AUTH_ANONYMOUS_ORG_ROLE: Admin
      GF_AUTH_DISABLE_LOGIN_FORM: "true"
    volumes:
      - ./deploy/observability/grafana/provisioning:/etc/grafana/provisioning:ro
      - ./deploy/observability/grafana/dashboards:/var/lib/grafana/dashboards:ro
    ports:
      - "3000:3000"
    depends_on:
      - prometheus
      - jaeger
```
5. Первую строку-комментарий файла заменить на:
```yaml
# Локальное окружение: PostgreSQL, миграции, API, воркер, заглушки банков, Prometheus, Jaeger и Grafana.
```

Run: `docker compose config -q`
Expected: команда ничего не печатает и завершается с кодом 0.

- [ ] **Step 8: Ссылки в демо**

В `scripts/demo.sh` блок
```bash
  if [ "$(jq -r .status <<<"$RESP")" = "completed" ]; then
    jq . <<<"$RESP"
    exit 0
  fi
```
заменить на:
```bash
  if [ "$(jq -r .status <<<"$RESP")" = "completed" ]; then
    jq . <<<"$RESP"
    TAGS=$(jq -rn --arg id "$ID" '{"application.id": $id} | tojson | @uri')
    echo
    echo "Дашборд: ${GRAFANA:-http://localhost:3000}/d/loan-gateway"
    echo "Трейс:   ${JAEGER:-http://localhost:16686}/search?service=gateway-api&tags=$TAGS"
    exit 0
  fi
```

Run: `bash -n scripts/demo.sh`
Expected: без вывода, код 0.

- [ ] **Step 9: Проверка конфигов в CI и документация для агентов**

В `.github/workflows/ci.yml` после шага `Test` добавить:
```yaml
      - name: Observability configs
        run: make obs-check
```

В `CLAUDE.md`:
- в разделе «Команды» после строки про `make demo` добавить: ``- `make obs-check` — конфиг Prometheus, тесты алертов, JSON дашборда (нужен Docker)``;
- в разделе «Карта пакетов» в конец добавить: ``- `deploy/observability` — конфиги Prometheus, алерты с тестами, provisioning и дашборд Grafana``.

- [ ] **Step 10: Проверка и commit**

```bash
make obs-check && make lint && make test
git add deploy compose.yaml scripts/demo.sh Makefile .github/workflows/ci.yml CLAUDE.md
git commit -m "feat: add Prometheus, alerts, Grafana and Jaeger to compose"
```

---

### Task 6: Сборка в `cmd/gateway`: `/metrics`, трейсинг, `METRICS_ADDR`

**Ветка:** `feature/gateway-telemetry` · **Этап 3, локально, после merge Task 3, Task 4 и Task 5** · **Спека:** 9, 14.1, 14.4

**Files:**
- Create: `cmd/gateway/metrics.go`, `cmd/gateway/metrics_test.go`
- Modify: `internal/config/config.go`, `internal/config/config_test.go`, `cmd/gateway/main.go`, `cmd/gateway/api.go`, `cmd/gateway/worker.go`

**Interfaces:**
- Consumes: `telemetry.NewRegistry`, `NewMetrics`, `Handler`, `SetupTracing`, `Tracer`, `NewLogHandler`, `(*Metrics).RegisterQueueStats` (Task 1); `(*postgres.Queue).Stats` (Task 2); `httpapi.Deps.Metrics/Tracer` (Task 3); `dispatch.Config.Metrics/Tracer`, `dispatch.DefaultDBTimeout` (Task 4).
- Produces: `config.Gateway.MetricsAddr string` (env `METRICS_ADDR`, по умолчанию `:9100`); `func newMetricsServer(addr string, g prometheus.Gatherer) *http.Server`; `func startMetrics(addr string, g prometheus.Gatherer) (<-chan error, func(context.Context) error)`

- [ ] **Step 1: Создать ветку**

```bash
git switch develop && git pull --ff-only && git switch -c feature/gateway-telemetry
```

- [ ] **Step 2: Тесты конфигурации**

В `internal/config/config_test.go`:
- в `TestLoadGatewayDefaults` в `want` после `HTTPAddr: ":8080",` добавить `MetricsAddr: ":9100",`;
- в `TestLoadGatewayOverrides` в карту окружения добавить `"METRICS_ADDR": ":9200",`, а в условие проверки — `|| cfg.MetricsAddr != ":9200"`.

Run: `go test ./internal/config/`
Expected: FAIL — `unknown field MetricsAddr in struct literal of type Gateway`.

- [ ] **Step 3: METRICS_ADDR**

В `internal/config/config.go`:
- в `type Gateway struct` после `HTTPAddr` добавить `MetricsAddr     string`;
- в `LoadGateway` после `HTTPAddr: r.str("HTTP_ADDR", ":8080"),` добавить `MetricsAddr:     r.str("METRICS_ADDR", ":9100"),`.

Run: `go test ./internal/config/`
Expected: PASS.

- [ ] **Step 4: Падающие тесты сервера метрик**

Файл: `cmd/gateway/metrics_test.go`
```go
package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"
)

func TestMetricsServerServesRegistry(t *testing.T) {
	reg := telemetry.NewRegistry()
	telemetry.NewMetrics(reg).LeaseLost()
	srv := newMetricsServer(":0", reg)
	if srv.ReadHeaderTimeout <= 0 {
		t.Fatal("ReadHeaderTimeout is not set: a slow client holds a goroutine forever")
	}
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "loangw_lease_lost_total 1") || !strings.Contains(body, "go_goroutines") {
		t.Fatalf("code %d, body:\n%s", rec.Code, body)
	}
}

func TestStartMetricsReportsBusyPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	errs, stop := startMetrics(ln.Addr().String(), telemetry.NewRegistry())
	defer func() { _ = stop(context.Background()) }()
	select {
	case err := <-errs:
		if err == nil {
			t.Fatal("nil error for a busy port")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("busy METRICS_ADDR is not reported")
	}
}
```

Run: `go test ./cmd/gateway/`
Expected: FAIL — `undefined: newMetricsServer`.

- [ ] **Step 5: Сервер метрик**

Файл: `cmd/gateway/metrics.go`
```go
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"
)

// newMetricsServer — служебный HTTP-сервер с GET /metrics на METRICS_ADDR (раздел 14.1 спеки).
func newMetricsServer(addr string, g prometheus.Gatherer) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", telemetry.Handler(g))
	return &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
}

// startMetrics поднимает сервер метрик в фоне. Ошибка запуска (например, занятый порт) приходит
// в канал; stop останавливает сервер в пределах ctx.
func startMetrics(addr string, g prometheus.Gatherer) (<-chan error, func(context.Context) error) {
	srv := newMetricsServer(addr, g)
	errs := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errs <- fmt.Errorf("metrics server: %w", err)
		}
	}()
	return errs, srv.Shutdown
}
```

Run: `go test ./cmd/gateway/`
Expected: PASS.

- [ ] **Step 6: trace_id в логах**

В `cmd/gateway/main.go` добавить импорт `"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"` и строку создания логгера заменить на:
```go
	log := slog.New(telemetry.NewLogHandler(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel})))
```

- [ ] **Step 7: API — метрики и трейсинг**

Файл `cmd/gateway/api.go` заменить целиком:
```go
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/config"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/httpapi"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/store/postgres"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"
)

// runAPI обслуживает HTTP API до отмены ctx, затем корректно останавливает сервер, сервер
// метрик и трейсинг в пределах SHUTDOWN_TIMEOUT.
func runAPI(ctx context.Context, cfg config.Gateway, log *slog.Logger) error {
	reg, hooks, err := buildBanks(cfg)
	if err != nil {
		return err
	}
	shutdownTracing, err := telemetry.SetupTracing(ctx, os.Getenv, "gateway-api", log)
	if err != nil {
		return err
	}
	pool, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	metricsReg := telemetry.NewRegistry()
	handler := httpapi.NewHandler(httpapi.Deps{
		Applications:   application.NewService(postgres.NewApplications(pool), reg.Codes(), time.Now),
		Callbacks:      postgres.NewQueue(pool),
		DB:             pool,
		WebhookBanks:   hooks,
		WebhookMaxSkew: cfg.WebhookMaxSkew,
		Log:            log,
		Metrics:        telemetry.NewMetrics(metricsReg),
		Tracer:         telemetry.Tracer(),
	})
	srv := newAPIServer(cfg.HTTPAddr, handler)
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	metricsErr, stopMetrics := startMetrics(cfg.MetricsAddr, metricsReg)
	log.Info("api started", "addr", cfg.HTTPAddr, "metrics_addr", cfg.MetricsAddr, "banks", reg.Codes())

	var runErr error
	select {
	case runErr = <-errCh:
	case runErr = <-metricsErr:
	case <-ctx.Done():
	}
	log.Info("api stopping")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	return errors.Join(runErr, srv.Shutdown(shutdownCtx), stopMetrics(shutdownCtx), shutdownTracing(shutdownCtx))
}

// newAPIServer — HTTP-сервер API. Лимит тела ограничивает размер, но не время: без ReadTimeout
// медленный клиент и простаивающее keep-alive-соединение держат горутину сколь угодно долго.
func newAPIServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}
```

- [ ] **Step 8: Воркер — метрики, gauge очереди и трейсинг**

Файл `cmd/gateway/worker.go` заменить целиком:
```go
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/config"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/dispatch"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/store/postgres"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"
)

// runWorker обрабатывает очередь до отмены ctx; сервер метрик и трейсинг останавливаются
// вместе с воркером в пределах SHUTDOWN_TIMEOUT.
func runWorker(ctx context.Context, cfg config.Gateway, log *slog.Logger) error {
	reg, _, err := buildBanks(cfg)
	if err != nil {
		return err
	}
	shutdownTracing, err := telemetry.SetupTracing(ctx, os.Getenv, "gateway-worker", log)
	if err != nil {
		return err
	}
	pool, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	queue := postgres.NewQueue(pool)
	metricsReg := telemetry.NewRegistry()
	metrics := telemetry.NewMetrics(metricsReg)
	metrics.RegisterQueueStats(metricsReg, queue.Stats, dispatch.DefaultDBTimeout)
	w := dispatch.New(queue, reg, dispatch.Config{
		PollInterval:    cfg.PollInterval,
		BatchSize:       cfg.BatchSize,
		Concurrency:     cfg.Concurrency,
		Lease:           cfg.Lease,
		MaxAttempts:     cfg.MaxAttempts,
		BaseDelay:       cfg.BaseDelay,
		MaxDelay:        cfg.MaxDelay,
		CallbackTimeout: cfg.CallbackTimeout,
		SweepInterval:   cfg.SweepInterval,
		ShutdownTimeout: cfg.ShutdownTimeout,
		BankTimeouts:    bankTimeouts(cfg),
		Metrics:         metrics,
		Tracer:          telemetry.Tracer(),
	}, log)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	metricsErr, stopMetrics := startMetrics(cfg.MetricsAddr, metricsReg)
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	var exitErr error
	select {
	case exitErr = <-metricsErr: // без метрик воркер не работает: занятый порт — ошибка конфигурации
		cancel()
		<-done
	case exitErr = <-done:
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancelShutdown()
	return errors.Join(exitErr, stopMetrics(shutdownCtx), shutdownTracing(shutdownCtx))
}
```

- [ ] **Step 9: Запустить тесты и смоук в compose**

```bash
make lint && make test
make up
curl -sf localhost:8080/readyz && make demo
docker compose exec -T prometheus wget -qO- 'http://localhost:9090/api/v1/targets' | jq -r '.data.activeTargets[] | .labels.job + " " + .health'
make down
```

Expected: тесты зелёные; `make demo` печатает решения трёх банков и две ссылки; цели Prometheus — `api up` и `worker up`.

- [ ] **Step 10: Commit**

```bash
git add cmd/gateway internal/config
git commit -m "feat(cmd): serve metrics and set up tracing in api and worker"
```

---

### Task 7: E2E наблюдаемости, README и релиз v0.2.0

**Ветка:** `feature/observability-e2e`, затем `release/0.2.0` · **Этап 3, локально, после merge Task 6** · **Спека:** 14.6, 14.7, 13.1, 13.5

**Files:**
- Create: `test/e2e/observability_test.go`
- Modify: `test/e2e/harness_test.go`, `README.md`, `docs/dev-log.md`

**Interfaces:**
- Consumes: всё из Task 1–6; `start`, `options`, `(*env).submit`, `northbankMock`, `eastbankMock`, `westbankMock`, `sampleInput` (e2e v0.1.0)
- Produces: поля `env.spans *tracetest.SpanRecorder`, `env.apiMetrics`, `env.workerMetrics *prometheus.Registry`; релиз `v0.2.0` в `main`

- [ ] **Step 1: Создать ветку**

```bash
git switch develop && git pull --ff-only && git switch -c feature/observability-e2e
```

- [ ] **Step 2: Подключить трейсер и метрики в e2e-окружение**

В `test/e2e/harness_test.go`:
1. Импорты: добавить `"github.com/prometheus/client_golang/prometheus"`, `"go.opentelemetry.io/otel/sdk/trace/tracetest"`, `"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"` и `"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry/telemetrytest"`.
2. В `type env struct` после `client` добавить:
```go
	spans         *tracetest.SpanRecorder // завершённые спаны API и воркера
	apiMetrics    *prometheus.Registry    // метрики процесса api
	workerMetrics *prometheus.Registry    // метрики процесса worker
```
3. В `start` после создания `log` добавить:
```go
	tracer, spans := telemetrytest.Tracer()
	apiReg, workerReg := telemetry.NewRegistry(), telemetry.NewRegistry()
	workerMetrics := telemetry.NewMetrics(workerReg)
```
4. В `start` в `httpapi.Deps{...}` после `Log: log,` добавить:
```go
		Metrics:        telemetry.NewMetrics(apiReg),
		Tracer:         tracer,
```
5. В `start` перед `worker := dispatch.New(` добавить `workerMetrics.RegisterQueueStats(workerReg, queue.Stats, time.Second)`, а в `dispatch.Config{...}` после `BankTimeouts: timeouts,` добавить:
```go
		Metrics:         workerMetrics,
		Tracer:          tracer,
```
6. Возврат из `start` заменить на:
```go
	return &env{t: t, url: apiURL, pool: pool, client: &http.Client{Timeout: 5 * time.Second},
		spans: spans, apiMetrics: apiReg, workerMetrics: workerReg}
```

Run: `go test -race ./test/e2e/`
Expected: PASS — прежние e2e-тесты не замечают трейсер и метрики.

- [ ] **Step 3: Написать e2e-тесты наблюдаемости**

Файл: `test/e2e/observability_test.go`
```go
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
			texts = append(texts, kv.Value.Emit())
		}
		for _, ev := range s.Events() {
			texts = append(texts, ev.Name)
			for _, kv := range ev.Attributes {
				texts = append(texts, kv.Value.Emit())
			}
		}
		for _, l := range s.Links() {
			for _, kv := range l.Attributes {
				texts = append(texts, kv.Value.Emit())
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
```

- [ ] **Step 4: Запустить**

Run: `go test -race -count=1 ./test/e2e/`
Expected: PASS. Код написан в Task 1–6; падение — дефект интеграции, разбирать по `superpowers:systematic-debugging`.

- [ ] **Step 5: README**

В `README.md`:
1. После раздела «Быстрый старт» добавить раздел:
````markdown
## Наблюдаемость

`make up` поднимает и стек наблюдаемости, `make demo` в конце печатает ссылки на дашборд и трейс заявки.

| Что | Где |
|---|---|
| Дашборд Grafana «Loan Gateway»: нагрузка API, задержка и исходы по банкам, очередь, вебхуки | http://localhost:3000/d/loan-gateway |
| Prometheus: метрики и алерты (`TargetDown`, `BankErrorRateHigh`, `QueueBacklog`, `CallbackTimeouts`) | http://localhost:9090/alerts |
| Jaeger: трейс заявки — API, все попытки по каждому банку и вебхук westbank в одном трейсе | http://localhost:16686 |

Метрики `api` и `worker` — на `:9100/metrics`. В логах записи о запросах и задачах содержат `trace_id`. Подробно — раздел 14 [спецификации](docs/spec.md).
````
2. В разделе «Как это работает» в конец списка добавить пункт:
```markdown
- Метрики Prometheus и трейсы OpenTelemetry показывают состояние шлюза и путь каждой заявки через очередь.
```
3. В разделе «Как разрабатывался проект» после пункта 4 добавить пункт:
```markdown
5. v0.2.0 (наблюдаемость) — тем же процессом: раздел 14 спеки, план `docs/plan-observability.md`, 7 задач; основа и сборка локально, HTTP API, воркер и инфраструктура — в трёх параллельных облачных сессиях.
```
4. Строку «Итоги по журналу разработки…» переписать по журналу на момент релиза. Задачи — число строк вида `| 20…`, относящихся к задачам планов; PR — `gh pr list --state all --base develop --limit 200 --json headRefName --jq '[.[]|select(.headRefName|startswith("feature/"))]|length'`; замечания — сумма столбца (`awk -F'|' '/^\| 20/ {split($6,a,"/"); f+=a[1]; t+=a[2]} END {print f, t}' docs/dev-log.md`); стоимость — сумма облачных сессий из журнала:
```markdown
Итоги по [журналу разработки](docs/dev-log.md): задач в планах — 25 (v0.1.0 — 18, v0.2.0 — 7), PR из feature-веток — <N>, замечаний ревью исправлено — <исправлено> из <всего>, облачные сессии — $<сумма>.
```

- [ ] **Step 6: Журнал, commit и merge**

Добавить в `docs/dev-log.md` строку о v0.2.0 Task 7, затем:
```bash
make lint && make test
git add test/e2e README.md docs/dev-log.md
git commit -m "test(e2e): cover traces, personal data and metrics; describe observability"
```

Влить `feature/observability-e2e` в `develop` по разделу «Процесс выполнения задачи» (merge — человек).

- [ ] **Step 7: Финальная проверка на develop**

```bash
git switch develop && git pull --ff-only
make lint && make test && make obs-check
make up && make demo
docker compose exec -T prometheus wget -qO- 'http://localhost:9090/api/v1/targets' | jq -r '.data.activeTargets[] | .labels.job + " " + .health'
curl -s localhost:3000/api/dashboards/uid/loan-gateway | jq -r .dashboard.title
```

Expected: всё зелёное; `make demo` показывает три банка `approved` и две ссылки; цели `api up` и `worker up`; заголовок дашборда — `Loan Gateway`. Открыть ссылку на трейс из демо: в трейсе есть `POST /v1/applications`, три попытки eastbank (2 — 503) и по одной у northbank и westbank, `bank_request.callback`. Затем `make down`. Критерии готовности 14.7 выполнены.

- [ ] **Step 8: Релиз по gitflow**

```bash
git switch develop && git pull --ff-only
git switch -c release/0.2.0
git push -u origin release/0.2.0
gh pr create --base main --head release/0.2.0 --title "Release 0.2.0" --body "Наблюдаемость: метрики Prometheus, трейсинг OpenTelemetry, дашборд Grafana и алерты (раздел 14 спеки)."
```

После зелёного CI человек мержит PR, затем:
```bash
git switch main && git pull --ff-only
git tag -a v0.2.0 -m "Loan Gateway 0.2.0: observability"
git push origin v0.2.0
gh pr create --base develop --head main --title "Merge release 0.2.0 back into develop" --body "Gitflow: main → develop после релиза."
```
