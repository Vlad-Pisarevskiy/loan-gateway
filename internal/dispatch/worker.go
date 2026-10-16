package dispatch

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

// DefaultBankTimeout — таймаут вызова банка, если он не задан в Config.BankTimeouts.
const DefaultBankTimeout = 5 * time.Second

// DefaultDBTimeout — таймаут одного обращения к очереди, если Config.DBTimeout не задан.
const DefaultDBTimeout = 5 * time.Second

// Config — параметры воркера (раздел 9 спеки).
type Config struct {
	PollInterval    time.Duration
	BatchSize       int
	Concurrency     int
	Lease           time.Duration
	MaxAttempts     int
	BaseDelay       time.Duration
	MaxDelay        time.Duration
	CallbackTimeout time.Duration
	SweepInterval   time.Duration
	ShutdownTimeout time.Duration
	BankTimeouts    map[string]time.Duration // таймаут вызова по коду банка
	DBTimeout       time.Duration            // таймаут одного обращения к очереди; 0 — DefaultDBTimeout
	Metrics         *telemetry.Metrics       // nil — метрики не пишутся
	Tracer          trace.Tracer             // nil — спаны не создаются
}

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
	cfg.Metrics.InitWorkerSeries(banks.Codes())
	return &Worker{queue: queue, banks: banks, cfg: cfg, log: log, rnd: rand.Int64N, tracer: tracer}
}

// Run обрабатывает очередь до отмены ctx. После отмены перестаёт брать задачи и ждёт текущие
// не дольше ShutdownTimeout, затем отменяет вызовы банков; их результаты записываются как
// временные ошибки, а то, что записать не удалось, вернёт чистильщик.
func (w *Worker) Run(ctx context.Context) error {
	// У задач свой контекст: отмена ctx не должна обрывать вызовы банков сразу.
	taskCtx, cancelTasks := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelTasks()

	slots := make(chan struct{}, w.cfg.Concurrency)
	var wg sync.WaitGroup
	poll := time.NewTicker(w.cfg.PollInterval)
	defer poll.Stop()
	sweep := time.NewTicker(w.cfg.SweepInterval)
	defer sweep.Stop()

	w.log.Info("worker started", "concurrency", w.cfg.Concurrency)
	for {
		select {
		case <-ctx.Done():
			w.drain(&wg, cancelTasks)
			w.log.Info("worker stopped")
			return nil
		case <-sweep.C:
			w.sweep(ctx)
		case <-poll.C:
			w.poll(ctx, taskCtx, slots, &wg)
		}
	}
}

// poll забирает столько задач, сколько свободных слотов, и запускает их обработку.
func (w *Worker) poll(ctx, taskCtx context.Context, slots chan struct{}, wg *sync.WaitGroup) {
	limit := min(w.cfg.BatchSize, cap(slots)-len(slots))
	if limit <= 0 {
		return
	}
	claimCtx, cancel := context.WithTimeout(ctx, w.dbTimeout())
	defer cancel()
	tasks, err := w.queue.Claim(claimCtx, limit, w.cfg.Lease)
	if err != nil {
		if ctx.Err() == nil {
			w.cfg.Metrics.QueueError(telemetry.OpClaim)
			w.log.Error("claim failed", "error", err)
		}
		return
	}
	for _, t := range tasks {
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			w.process(taskCtx, t)
		})
	}
}

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

// drain ждёт текущие задачи не дольше ShutdownTimeout, затем отменяет их и дожидается выхода.
func (w *Worker) drain(wg *sync.WaitGroup, cancelTasks context.CancelFunc) {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	timer := time.NewTimer(w.cfg.ShutdownTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		w.log.Warn("shutdown timeout: cancelling in-flight bank calls")
		cancelTasks()
		<-done
	}
}

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

func (w *Worker) dbTimeout() time.Duration {
	if w.cfg.DBTimeout > 0 {
		return w.cfg.DBTimeout
	}
	return DefaultDBTimeout
}

func (w *Worker) bankTimeout(code string) time.Duration {
	if d, ok := w.cfg.BankTimeouts[code]; ok && d > 0 {
		return d
	}
	return DefaultBankTimeout
}
