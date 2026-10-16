// Package e2e — end-to-end тесты: API и воркер в процессе, заглушки банков через httptest,
// PostgreSQL в Docker. Пропускаются при -short.
package e2e

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/dispatch"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/httpapi"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/store/postgres"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/store/postgres/pgtest"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry/telemetrytest"
)

// mockBank описывает банк в e2e-окружении.
type mockBank struct {
	code    string
	mock    func(apiURL string) http.Handler               // заглушка; apiURL — адрес шлюза для вебхуков
	adapter func(mockURL string, c *http.Client) bank.Bank // адаптер шлюза, направленный на заглушку
	secret  []byte                                         // секрет вебхуков асинхронного банка
	timeout time.Duration                                  // таймаут вызова; 0 — 500 мс
}

type options struct {
	banks           []mockBank
	maxAttempts     int           // 0 — 5
	callbackTimeout time.Duration // 0 — 5 с
}

type env struct {
	t             *testing.T
	url           string
	pool          *pgxpool.Pool
	client        *http.Client            // клиент к API с таймаутом, чтобы зависший API не держал тест до общего таймаута
	spans         *tracetest.SpanRecorder // завершённые спаны API и воркера
	apiMetrics    *prometheus.Registry    // метрики процесса api
	workerMetrics *prometheus.Registry    // метрики процесса worker
}

// start поднимает шлюз (API и воркер) с заглушками банков; всё останавливается в t.Cleanup.
func start(t *testing.T, o options) *env {
	t.Helper()
	pool := pgtest.NewPool(t)
	log := slog.New(slog.NewTextHandler(t.Output(), &slog.HandlerOptions{Level: slog.LevelWarn}))
	tracer, spans := telemetrytest.Tracer()
	apiReg, workerReg := telemetry.NewRegistry(), telemetry.NewRegistry()
	workerMetrics := telemetry.NewMetrics(workerReg)

	// Адрес API нужен заглушкам для вебхуков раньше, чем собран его обработчик.
	api := httptest.NewUnstartedServer(nil)
	apiURL := "http://" + api.Listener.Addr().String()

	client := &http.Client{}
	var banks []bank.Bank
	hooks := map[string]httpapi.WebhookBank{}
	timeouts := map[string]time.Duration{}
	for _, mb := range o.banks {
		mock := httptest.NewServer(mb.mock(apiURL))
		t.Cleanup(mock.Close)
		b := mb.adapter(mock.URL, client)
		banks = append(banks, b)
		if h, ok := b.(bank.CallbackHandler); ok {
			hooks[mb.code] = httpapi.WebhookBank{Handler: h, Secret: mb.secret}
		}
		timeouts[mb.code] = cmp.Or(mb.timeout, 500*time.Millisecond)
	}
	reg, err := bank.NewRegistry(banks...)
	if err != nil {
		t.Fatal(err)
	}

	queue := postgres.NewQueue(pool)
	api.Config.Handler = httpapi.NewHandler(httpapi.Deps{
		Applications:   application.NewService(postgres.NewApplications(pool), reg.Codes(), time.Now),
		Callbacks:      queue,
		DB:             pool,
		WebhookBanks:   hooks,
		WebhookMaxSkew: 5 * time.Minute,
		Log:            log,
		Metrics:        telemetry.NewMetrics(apiReg),
		Tracer:         tracer,
	})
	api.Start()
	t.Cleanup(api.Close)

	workerMetrics.RegisterQueueStats(workerReg, queue.Stats, time.Second, reg.Codes())
	worker := dispatch.New(queue, reg, dispatch.Config{
		PollInterval:    20 * time.Millisecond,
		BatchSize:       10,
		Concurrency:     10,
		Lease:           5 * time.Second,
		MaxAttempts:     cmp.Or(o.maxAttempts, 5),
		BaseDelay:       10 * time.Millisecond,
		MaxDelay:        50 * time.Millisecond,
		CallbackTimeout: cmp.Or(o.callbackTimeout, 5*time.Second),
		SweepInterval:   50 * time.Millisecond,
		ShutdownTimeout: 2 * time.Second,
		BankTimeouts:    timeouts,
		Metrics:         workerMetrics,
		Tracer:          tracer,
	}, log)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var runErr error
	go func() {
		defer close(done)
		runErr = worker.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			t.Errorf("worker: %v", runErr)
		}
	})
	return &env{t: t, url: apiURL, pool: pool, client: &http.Client{Timeout: 5 * time.Second},
		spans: spans, apiMetrics: apiReg, workerMetrics: workerReg}
}

type offerView struct {
	Amount         int64 `json:"amount"`
	RateBPS        int   `json:"rate_bps"`
	TermMonths     int   `json:"term_months"`
	MonthlyPayment int64 `json:"monthly_payment"`
}

type bankView struct {
	BankCode     string     `json:"bank_code"`
	Status       string     `json:"status"`
	Attempts     int        `json:"attempts"`
	Offer        *offerView `json:"offer"`
	RejectReason string     `json:"reject_reason"`
	Error        string     `json:"error"`
}

type applicationView struct {
	ID     string     `json:"id"`
	Status string     `json:"status"`
	Banks  []bankView `json:"banks"`
}

// bank возвращает результат по банку code.
func (v applicationView) bank(t *testing.T, code string) bankView {
	t.Helper()
	for _, b := range v.Banks {
		if b.BankCode == code {
			return b
		}
	}
	t.Fatalf("no result for bank %s in %+v", code, v)
	return bankView{}
}

// sampleInput — анкета из раздела 5.1 спеки.
func sampleInput() map[string]any {
	return map[string]any{
		"full_name":       "Иванов Иван Иванович",
		"birth_date":      "1990-05-17",
		"phone":           "+79991234567",
		"monthly_income":  12_000_000,
		"employment_type": "employed",
		"amount":          30_000_000,
		"term_months":     24,
	}
}

// post отправляет заявку и возвращает код ответа и тело (для 202).
func (e *env) post(key string, body map[string]any) (int, applicationView) {
	e.t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		e.t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, e.url+"/v1/applications", bytes.NewReader(payload))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	var v applicationView
	if resp.StatusCode == http.StatusAccepted {
		if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
			e.t.Fatalf("decode POST response: %v", err)
		}
	} else {
		_, _ = io.Copy(io.Discard, resp.Body)
	}
	return resp.StatusCode, v
}

// get читает заявку.
func (e *env) get(id string) applicationView {
	e.t.Helper()
	resp, err := e.client.Get(e.url + "/v1/applications/" + id)
	if err != nil {
		e.t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("GET returned %d", resp.StatusCode)
	}
	var v applicationView
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		e.t.Fatalf("decode GET response: %v", err)
	}
	return v
}

// waitCompleted опрашивает заявку, пока она не станет completed (не дольше 10 с).
func (e *env) waitCompleted(id string) applicationView {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		v := e.get(id)
		if v.Status == "completed" {
			return v
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("application %s is not completed in 10s: %+v", id, v)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// submit отправляет sampleInput, изменённый edit, и ждёт завершения заявки.
func (e *env) submit(edit func(map[string]any)) applicationView {
	e.t.Helper()
	in := sampleInput()
	if edit != nil {
		edit(in)
	}
	code, created := e.post(uuid.NewString(), in)
	if code != http.StatusAccepted {
		e.t.Fatalf("POST returned %d, want 202", code)
	}
	return e.waitCompleted(created.ID)
}
