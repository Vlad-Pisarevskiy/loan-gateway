package dispatch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
)

type completion struct {
	task    Task
	outcome Outcome
}

// fakeQueue — очередь в памяти. Флаги hang* задаются до запуска воркера и не меняются.
type fakeQueue struct {
	mu           sync.Mutex
	pending      []Task
	claimErrs    int        // сколько первых вызовов Claim вернут ошибку
	hangDB       bool       // Claim и Sweep висят до отмены ctx, как запрос к зависшей БД
	hangComplete bool       // Complete висит до отмены ctx или закрытия unblock
	lostLease    bool       // Complete сообщает, что аренду перехватили
	completeErr  error      // Complete возвращает эту ошибку
	sweepStats   SweepStats // что возвращает Sweep
	unblock      chan struct{}
	limits       []int
	completed    []completion
	sweeps       int
}

func (q *fakeQueue) Claim(ctx context.Context, limit int, _ time.Duration) ([]Task, error) {
	if q.hangDB {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.limits = append(q.limits, limit)
	if q.claimErrs > 0 {
		q.claimErrs--
		return nil, errors.New("connection refused")
	}
	n := min(limit, len(q.pending))
	tasks := slices.Clone(q.pending[:n])
	q.pending = q.pending[n:]
	return tasks, nil
}

func (q *fakeQueue) Complete(ctx context.Context, t Task, o Outcome) (bool, error) {
	if q.hangComplete {
		select {
		case <-ctx.Done():
		case <-q.unblock:
		}
	}
	if err := ctx.Err(); err != nil { // как pgx: с отменённым контекстом запрос не выполняется
		return false, err
	}
	if q.completeErr != nil {
		return false, q.completeErr
	}
	if q.lostLease {
		return false, nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.completed = append(q.completed, completion{task: t, outcome: o})
	return true, nil
}

func (q *fakeQueue) Sweep(ctx context.Context, _ int) (SweepStats, error) {
	if q.hangDB {
		<-ctx.Done()
		return SweepStats{}, ctx.Err()
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.sweeps++
	return q.sweepStats, nil
}

func (q *fakeQueue) completions() []completion {
	q.mu.Lock()
	defer q.mu.Unlock()
	return slices.Clone(q.completed)
}

func (q *fakeQueue) claimLimits() []int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return slices.Clone(q.limits)
}

func (q *fakeQueue) pendingCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

func (q *fakeQueue) sweepCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.sweeps
}

type submitFunc func(ctx context.Context, req bank.SubmitRequest) (bank.SubmitResult, error)

type fakeBank struct {
	code   string
	submit submitFunc
}

func (b fakeBank) Code() string { return b.code }

func (b fakeBank) Submit(ctx context.Context, req bank.SubmitRequest) (bank.SubmitResult, error) {
	return b.submit(ctx, req)
}

var approved = application.Decision{Approved: true, Amount: 30_000_000, RateBPS: 1590, TermMonths: 24, MonthlyPayment: 1_467_460}

func decide(context.Context, bank.SubmitRequest) (bank.SubmitResult, error) {
	d := approved
	return bank.SubmitResult{Decision: &d}, nil
}

func failWith(err error) submitFunc {
	return func(context.Context, bank.SubmitRequest) (bank.SubmitResult, error) {
		return bank.SubmitResult{}, err
	}
}

func testConfig() Config {
	return Config{
		PollInterval:    5 * time.Millisecond,
		BatchSize:       10,
		Concurrency:     4,
		Lease:           time.Minute,
		MaxAttempts:     3,
		BaseDelay:       10 * time.Millisecond,
		MaxDelay:        100 * time.Millisecond,
		CallbackTimeout: time.Minute,
		SweepInterval:   5 * time.Millisecond,
		ShutdownTimeout: time.Second,
		BankTimeouts:    map[string]time.Duration{},
	}
}

func newTestWorker(t *testing.T, q Queue, cfg Config, banks ...bank.Bank) *Worker {
	t.Helper()
	reg, err := bank.NewRegistry(banks...)
	if err != nil {
		t.Fatal(err)
	}
	return New(q, reg, cfg, slog.New(slog.NewTextHandler(t.Output(), nil)))
}

// syncBuffer — буфер для логов, в который пишут несколько горутин.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// captureLogs перенаправляет логи воркера в буфер.
func captureLogs(w *Worker) *syncBuffer {
	var buf syncBuffer
	w.log = slog.New(slog.NewTextHandler(&buf, nil))
	return &buf
}

func newTask(code string, attempts int) Task {
	return Task{RequestID: uuid.New(), BankCode: code, Attempts: attempts, Application: application.Application{ID: uuid.New()}}
}

// runWorker запускает Run в фоне; возвращённая функция останавливает воркер и ждёт выхода.
func runWorker(t *testing.T, w *Worker) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	stop := sync.OnceFunc(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run: %v", err)
		}
	})
	t.Cleanup(stop)
	return stop
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestCallOutcomes(t *testing.T) {
	tests := []struct {
		name     string
		attempts int
		submit   submitFunc
		want     Outcome
	}{
		{
			name: "decision", attempts: 1, submit: decide,
			want: Outcome{Kind: OutcomeDecision, Decision: approved},
		},
		{
			name: "accepted asynchronously", attempts: 1,
			submit: func(context.Context, bank.SubmitRequest) (bank.SubmitResult, error) {
				return bank.SubmitResult{ExternalID: "w-1"}, nil
			},
			want: Outcome{Kind: OutcomeAccepted, ExternalID: "w-1", CallbackTimeout: time.Minute},
		},
		{
			name: "permanent error fails", attempts: 1, submit: failWith(bank.StatusError(400)),
			want: Outcome{Kind: OutcomeFailed, Error: "permanent bank error: unexpected status 400"},
		},
		{
			name: "temporary error on last attempt fails", attempts: 3, submit: failWith(bank.StatusError(503)),
			want: Outcome{Kind: OutcomeFailed, Error: "temporary bank error: unexpected status 503"},
		},
		{
			name: "empty result fails", attempts: 1,
			submit: func(context.Context, bank.SubmitRequest) (bank.SubmitResult, error) {
				return bank.SubmitResult{}, nil
			},
			want: Outcome{Kind: OutcomeFailed, Error: "bank returned neither decision nor external id"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newTestWorker(t, &fakeQueue{}, testConfig(), fakeBank{code: "northbank", submit: tt.submit})
			if got := w.call(t.Context(), newTask("northbank", tt.attempts)); got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestCallRetriesTemporaryErrors(t *testing.T) {
	for _, err := range []error{bank.StatusError(503), errors.New("unclassified")} {
		w := newTestWorker(t, &fakeQueue{}, testConfig(), fakeBank{code: "northbank", submit: failWith(err)})
		got := w.call(t.Context(), newTask("northbank", 2))
		if got.Kind != OutcomeRetry || got.Error != err.Error() {
			t.Fatalf("got %+v, want retry with %q", got, err)
		}
		if got.RetryAfter < 0 || got.RetryAfter > 20*time.Millisecond { // попытка 2: не больше base·2
			t.Fatalf("retry after %s, want within [0, 20ms]", got.RetryAfter)
		}
	}
}

func TestCallPassesRequestToBank(t *testing.T) {
	var got bank.SubmitRequest
	submit := func(ctx context.Context, req bank.SubmitRequest) (bank.SubmitResult, error) {
		got = req
		return decide(ctx, req)
	}
	w := newTestWorker(t, &fakeQueue{}, testConfig(), fakeBank{code: "northbank", submit: submit})
	task := newTask("northbank", 1)
	w.call(t.Context(), task)
	if got.RequestID != task.RequestID || got.Application.ID != task.Application.ID {
		t.Fatalf("bank got %+v, want request %s for application %s", got, task.RequestID, task.Application.ID)
	}
}

func TestCallUnknownBankFails(t *testing.T) {
	w := newTestWorker(t, &fakeQueue{}, testConfig(), fakeBank{code: "northbank", submit: decide})
	if got := w.call(t.Context(), newTask("nobank", 1)); got != (Outcome{Kind: OutcomeFailed, Error: "unknown bank"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestCallAppliesBankTimeout(t *testing.T) {
	cfg := testConfig()
	cfg.BankTimeouts["slowbank"] = 20 * time.Millisecond
	slow := func(ctx context.Context, _ bank.SubmitRequest) (bank.SubmitResult, error) {
		<-ctx.Done()
		return bank.SubmitResult{}, fmt.Errorf("%w: %w", bank.ErrTemporary, ctx.Err())
	}
	w := newTestWorker(t, &fakeQueue{}, cfg, fakeBank{code: "slowbank", submit: slow})
	start := time.Now()
	got := w.call(t.Context(), newTask("slowbank", 1))
	if got.Kind != OutcomeRetry {
		t.Fatalf("got %+v, want retry", got)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("call took %s, bank timeout is not applied", elapsed)
	}
}

func TestRunProcessesClaimedTasks(t *testing.T) {
	q := &fakeQueue{pending: []Task{newTask("northbank", 1), newTask("northbank", 1), newTask("northbank", 1)}}
	stop := runWorker(t, newTestWorker(t, q, testConfig(), fakeBank{code: "northbank", submit: decide}))
	eventually(t, "3 completions", func() bool { return len(q.completions()) == 3 })
	stop()
	for _, c := range q.completions() {
		if c.outcome.Kind != OutcomeDecision {
			t.Fatalf("outcome = %+v, want decision", c.outcome)
		}
	}
}

func TestRunKeepsWorkingAfterClaimError(t *testing.T) {
	q := &fakeQueue{pending: []Task{newTask("northbank", 1)}, claimErrs: 2}
	runWorker(t, newTestWorker(t, q, testConfig(), fakeBank{code: "northbank", submit: decide}))
	eventually(t, "completion after claim errors", func() bool { return len(q.completions()) == 1 })
}

func TestRunSurvivesHangingDatabase(t *testing.T) {
	cfg := testConfig()
	cfg.DBTimeout = 20 * time.Millisecond
	w := newTestWorker(t, &fakeQueue{hangDB: true}, cfg, fakeBank{code: "northbank", submit: decide})
	logs := captureLogs(w)
	runWorker(t, w)
	eventually(t, "claim and sweep failures in log", func() bool {
		s := logs.String()
		return strings.Contains(s, "claim failed") && strings.Contains(s, "sweep failed")
	})
}

func TestRunDoesNotHangOnSavingResult(t *testing.T) {
	cfg := testConfig()
	cfg.DBTimeout = 20 * time.Millisecond
	q := &fakeQueue{pending: []Task{newTask("northbank", 1)}, hangComplete: true, unblock: make(chan struct{})}
	w := newTestWorker(t, q, cfg, fakeBank{code: "northbank", submit: decide})
	logs := captureLogs(w)
	runWorker(t, w)
	t.Cleanup(func() { close(q.unblock) }) // при регрессии остановка не должна висеть до -timeout
	eventually(t, "save failure in log", func() bool {
		return strings.Contains(logs.String(), "save bank result failed")
	})
}

func TestRunSweeps(t *testing.T) {
	q := &fakeQueue{}
	runWorker(t, newTestWorker(t, q, testConfig(), fakeBank{code: "northbank", submit: decide}))
	eventually(t, "a sweep", func() bool { return q.sweepCount() > 0 })
}

func TestRunRespectsConcurrency(t *testing.T) {
	cfg := testConfig()
	cfg.Concurrency = 2
	var inFlight, maxInFlight atomic.Int32
	release := make(chan struct{})
	submit := func(ctx context.Context, req bank.SubmitRequest) (bank.SubmitResult, error) {
		n := inFlight.Add(1)
		for {
			m := maxInFlight.Load()
			if n <= m || maxInFlight.CompareAndSwap(m, n) {
				break
			}
		}
		select {
		case <-release:
		case <-ctx.Done(): // тест упал до close(release): не виснуть на остановке
		}
		inFlight.Add(-1)
		return decide(ctx, req)
	}
	q := &fakeQueue{}
	for range 5 {
		q.pending = append(q.pending, newTask("northbank", 1))
	}
	runWorker(t, newTestWorker(t, q, cfg, fakeBank{code: "northbank", submit: submit}))

	eventually(t, "2 tasks in flight", func() bool { return inFlight.Load() == 2 })
	time.Sleep(30 * time.Millisecond) // дать воркеру шанс взять лишнее, если ограничение сломано
	if n := q.pendingCount(); n != 3 {
		t.Fatalf("pending = %d, want 3: worker claimed beyond free slots", n)
	}
	close(release)
	eventually(t, "5 completions", func() bool { return len(q.completions()) == 5 })
	if m := maxInFlight.Load(); m > 2 {
		t.Fatalf("max in flight = %d, want <= 2", m)
	}
	for _, limit := range q.claimLimits() {
		if limit > 2 {
			t.Fatalf("claimed with limit %d, want <= free slots", limit)
		}
	}
}

func TestRunWaitsForInFlightTaskOnShutdown(t *testing.T) {
	started := make(chan struct{})
	ctxErr := make(chan error, 1)
	submit := func(ctx context.Context, req bank.SubmitRequest) (bank.SubmitResult, error) {
		close(started)
		time.Sleep(50 * time.Millisecond) // банк отвечает, не глядя на отмену
		ctxErr <- ctx.Err()
		return decide(ctx, req)
	}
	q := &fakeQueue{pending: []Task{newTask("northbank", 1)}}
	stop := runWorker(t, newTestWorker(t, q, testConfig(), fakeBank{code: "northbank", submit: submit}))
	<-started
	stop()
	if err := <-ctxErr; err != nil {
		t.Fatalf("bank call context = %v before ShutdownTimeout, want alive", err)
	}
	if n := len(q.completions()); n != 1 {
		t.Fatalf("completions after shutdown = %d, want 1", n)
	}
}

func TestRunCancelsInFlightTaskAfterShutdownTimeout(t *testing.T) {
	cfg := testConfig()
	cfg.ShutdownTimeout = 20 * time.Millisecond
	started := make(chan struct{})
	submit := func(ctx context.Context, _ bank.SubmitRequest) (bank.SubmitResult, error) {
		close(started)
		<-ctx.Done()
		return bank.SubmitResult{}, fmt.Errorf("%w: %w", bank.ErrTemporary, ctx.Err())
	}
	q := &fakeQueue{pending: []Task{newTask("northbank", 1)}}
	stop := runWorker(t, newTestWorker(t, q, cfg, fakeBank{code: "northbank", submit: submit}))
	<-started
	begin := time.Now()
	stop()
	if elapsed := time.Since(begin); elapsed < cfg.ShutdownTimeout || elapsed > time.Second {
		t.Fatalf("shutdown took %s, want about ShutdownTimeout %s", elapsed, cfg.ShutdownTimeout)
	}
	// Отменённый вызов записан как временная ошибка: задача уйдёт в повтор, не дожидаясь конца аренды.
	got := q.completions()
	if len(got) != 1 || got[0].outcome.Kind != OutcomeRetry {
		t.Fatalf("completions = %+v, want one retry", got)
	}
}
