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
	status := decisionRejected
	if approved {
		status = decisionApproved
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

// BankOutcomes — значения метки outcome у loangw_bank_calls_total; совпадают с dispatch.OutcomeKind.String().
var BankOutcomes = []string{"decision", "accepted", "retry", "failed"}

// Значения метки status у loangw_bank_decisions_total.
const (
	decisionApproved = "approved"
	decisionRejected = "rejected"
)

// decisionStatuses — все значения метки status у loangw_bank_decisions_total.
var decisionStatuses = []string{decisionApproved, decisionRejected}

// InitWorkerSeries создаёт с нулём серии воркера по банкам banks: серия счётчика иначе появляется
// при первом событии, и rate()/increase() это событие не видят (раздел 14.2 спеки).
func (m *Metrics) InitWorkerSeries(banks []string) {
	if m == nil {
		return
	}
	for _, b := range banks {
		for _, o := range BankOutcomes {
			m.bankCalls.WithLabelValues(b, o)
		}
		for _, s := range decisionStatuses {
			m.decisions.WithLabelValues(b, s, ViaSync)
		}
	}
}

// InitAPISeries — то же для api: вебхуки и решения по вебхукам асинхронных банков webhookBanks.
func (m *Metrics) InitAPISeries(webhookBanks []string) {
	if m == nil {
		return
	}
	for _, b := range webhookBanks {
		for _, r := range []string{WebhookApplied, WebhookDuplicate, WebhookUnknown, WebhookUnauthorized, WebhookBadBody, WebhookError} {
			m.webhooks.WithLabelValues(b, r)
		}
		for _, s := range decisionStatuses {
			m.decisions.WithLabelValues(b, s, ViaWebhook)
		}
	}
	m.webhooks.WithLabelValues(Other, WebhookUnknownBank)
}
