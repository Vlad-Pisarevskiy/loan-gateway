package dispatch

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
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
}

// Worker забирает задачи из очереди, вызывает банки и записывает результаты.
type Worker struct {
	queue Queue
	banks *bank.Registry
	cfg   Config
	log   *slog.Logger
	rnd   func(int64) int64
}

// New создаёт воркер.
func New(queue Queue, banks *bank.Registry, cfg Config, log *slog.Logger) *Worker {
	return &Worker{queue: queue, banks: banks, cfg: cfg, log: log, rnd: rand.Int64N}
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
			w.log.Error("sweep failed", "error", err)
		}
		return
	}
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

// process вызывает банк и записывает результат. В лог попадают только идентификаторы — без ПДн.
func (w *Worker) process(ctx context.Context, t Task) {
	log := w.log.With("bank_request_id", t.RequestID, "application_id", t.Application.ID,
		"bank_code", t.BankCode, "attempt", t.Attempts)
	out := w.call(ctx, t)
	// Запись не зависит от отмены задач: иначе ответ банка, полученный к ShutdownTimeout,
	// пропадёт, а задача провисит в sending до конца аренды. Таймаут — на случай зависшей БД.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.dbTimeout())
	defer cancel()
	ok, err := w.queue.Complete(saveCtx, t, out)
	switch {
	case err != nil:
		log.Error("save bank result failed", "outcome", out.Kind.String(), "error", err)
	case !ok:
		log.Warn("lease lost, bank result dropped", "outcome", out.Kind.String())
	case out.Kind == OutcomeRetry || out.Kind == OutcomeFailed:
		log.Warn("bank request failed", "outcome", out.Kind.String(), "error", out.Error, "retry_after", out.RetryAfter.String())
	default:
		log.Info("bank request processed", "outcome", out.Kind.String())
	}
}

// call вызывает банк с таймаутом и превращает ответ в Outcome.
func (w *Worker) call(ctx context.Context, t Task) Outcome {
	b, ok := w.banks.Get(t.BankCode)
	if !ok {
		return Outcome{Kind: OutcomeFailed, Error: "unknown bank"}
	}
	callCtx, cancel := context.WithTimeout(ctx, w.bankTimeout(t.BankCode))
	defer cancel()
	res, err := b.Submit(callCtx, bank.SubmitRequest{RequestID: t.RequestID, Application: t.Application})
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
