package postgres_test

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/dispatch"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/store/postgres"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/store/postgres/pgtest"
)

var approved = application.Decision{Approved: true, Amount: 30_000_000, RateBPS: 1590, TermMonths: 24, MonthlyPayment: 1_467_460}

// seed создаёт заявку с запросами в указанные банки.
func seed(t *testing.T, pool *pgxpool.Pool, codes ...string) application.Application {
	t.Helper()
	app := newApp()
	if _, _, err := postgres.NewApplications(pool).Create(t.Context(), app.ID.String(), []byte("h"), app, codes); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return app
}

func claim(t *testing.T, q *postgres.Queue, limit int) []dispatch.Task {
	t.Helper()
	tasks, err := q.Claim(t.Context(), limit, time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	return tasks
}

func complete(t *testing.T, q *postgres.Queue, task dispatch.Task, o dispatch.Outcome) bool {
	t.Helper()
	ok, err := q.Complete(t.Context(), task, o)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	return ok
}

// result возвращает состояние запроса заявки appID в банк code.
func result(t *testing.T, pool *pgxpool.Pool, appID uuid.UUID, code string) application.BankResult {
	t.Helper()
	v, err := postgres.NewApplications(pool).Get(t.Context(), appID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	for _, b := range v.Banks {
		if b.BankCode == code {
			return b
		}
	}
	t.Fatalf("no bank request for %s", code)
	return application.BankResult{}
}

func TestClaimReturnsTaskWithApplication(t *testing.T) {
	pool := pgtest.NewPool(t)
	q := postgres.NewQueue(pool)
	app := seed(t, pool, "northbank")

	tasks := claim(t, q, 10)
	if len(tasks) != 1 {
		t.Fatalf("claimed %d tasks, want 1", len(tasks))
	}
	task := tasks[0]
	if task.BankCode != "northbank" || task.Attempts != 1 || task.RequestID == uuid.Nil {
		t.Fatalf("task = %+v", task)
	}
	a := task.Application
	if a.ID != app.ID || a.FullName != app.FullName || !a.BirthDate.Equal(app.BirthDate) || a.Phone != app.Phone ||
		a.MonthlyIncome != app.MonthlyIncome || a.EmploymentType != app.EmploymentType ||
		a.Amount != app.Amount || a.TermMonths != app.TermMonths {
		t.Fatalf("application = %+v, want %+v", a, app)
	}
	if s := result(t, pool, app.ID, "northbank").Status; s != application.RequestSending {
		t.Fatalf("status = %s, want sending", s)
	}
	if again := claim(t, q, 10); len(again) != 0 {
		t.Fatalf("claimed %d tasks again, want 0", len(again))
	}
}

func TestClaimRespectsLimitAndSchedule(t *testing.T) {
	pool := pgtest.NewPool(t)
	q := postgres.NewQueue(pool)
	seed(t, pool, "eastbank", "northbank", "westbank")

	if got := claim(t, q, 2); len(got) != 2 {
		t.Fatalf("claimed %d tasks, want 2", len(got))
	}
	exec(t, pool, `UPDATE bank_requests SET next_attempt_at = now() + interval '1 hour' WHERE status = 'queued'`)
	if got := claim(t, q, 10); len(got) != 0 {
		t.Fatalf("claimed %d tasks scheduled for later, want 0", len(got))
	}
}

func TestConcurrentClaimsDoNotOverlap(t *testing.T) {
	pool := pgtest.NewPool(t)
	q := postgres.NewQueue(pool)
	for range 10 {
		seed(t, pool, "eastbank", "northbank")
	}
	var (
		mu   sync.Mutex
		seen = map[uuid.UUID]int{}
		wg   sync.WaitGroup
	)
	for range 4 {
		wg.Go(func() {
			tasks, err := q.Claim(t.Context(), 5, time.Minute)
			if err != nil {
				t.Errorf("Claim: %v", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, task := range tasks {
				seen[task.RequestID]++
			}
		})
	}
	wg.Wait()
	if len(seen) != 20 {
		t.Fatalf("claimed %d distinct tasks, want 20", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("task %s claimed %d times", id, n)
		}
	}
}

func TestCompleteWritesDecision(t *testing.T) {
	pool := pgtest.NewPool(t)
	q := postgres.NewQueue(pool)
	app := seed(t, pool, "northbank", "westbank")
	byCode := map[string]dispatch.Task{}
	for _, task := range claim(t, q, 2) {
		byCode[task.BankCode] = task
	}

	if !complete(t, q, byCode["northbank"], dispatch.Outcome{Kind: dispatch.OutcomeDecision, Decision: approved}) {
		t.Fatal("approve was not written")
	}
	reject := dispatch.Outcome{Kind: dispatch.OutcomeDecision, Decision: application.Decision{RejectReason: "debt_burden"}}
	if !complete(t, q, byCode["westbank"], reject) {
		t.Fatal("reject was not written")
	}

	north := result(t, pool, app.ID, "northbank")
	if north.Status != application.RequestApproved || north.Decision == nil || *north.Decision != approved {
		t.Fatalf("northbank = %+v, want approved with offer", north)
	}
	west := result(t, pool, app.ID, "westbank")
	if west.Status != application.RequestRejected || west.Decision == nil || west.Decision.RejectReason != "debt_burden" {
		t.Fatalf("westbank = %+v, want rejected with debt_burden", west)
	}
}

func TestCompleteAcceptedThenCallback(t *testing.T) {
	pool := pgtest.NewPool(t)
	q := postgres.NewQueue(pool)
	app := seed(t, pool, "westbank")
	task := claim(t, q, 1)[0]

	accepted := dispatch.Outcome{Kind: dispatch.OutcomeAccepted, ExternalID: "w-1", CallbackTimeout: time.Minute}
	if !complete(t, q, task, accepted) {
		t.Fatal("accepted outcome was not written")
	}
	if s := result(t, pool, app.ID, "westbank").Status; s != application.RequestWaitingCallback {
		t.Fatalf("status = %s, want waiting_callback", s)
	}

	apply := func(externalID string, d application.Decision) application.CallbackResult {
		t.Helper()
		res, err := q.ApplyCallback(t.Context(), "westbank", externalID, d)
		if err != nil {
			t.Fatalf("ApplyCallback: %v", err)
		}
		return res
	}
	if res := apply("w-1", approved); res != application.CallbackApplied {
		t.Fatalf("first callback = %d, want applied", res)
	}
	if b := result(t, pool, app.ID, "westbank"); b.Status != application.RequestApproved || *b.Decision != approved {
		t.Fatalf("westbank = %+v, want approved", b)
	}
	if res := apply("w-1", approved); res != application.CallbackDuplicate {
		t.Fatalf("repeated callback = %d, want duplicate", res)
	}
	if res := apply("w-unknown", approved); res != application.CallbackUnknown {
		t.Fatalf("unknown callback = %d, want unknown", res)
	}
}

func TestCallbackWithRejection(t *testing.T) {
	pool := pgtest.NewPool(t)
	q := postgres.NewQueue(pool)
	app := seed(t, pool, "westbank")
	complete(t, q, claim(t, q, 1)[0], dispatch.Outcome{Kind: dispatch.OutcomeAccepted, ExternalID: "w-2", CallbackTimeout: time.Minute})

	res, err := q.ApplyCallback(t.Context(), "westbank", "w-2", application.Decision{RejectReason: "insufficient_income"})
	if err != nil || res != application.CallbackApplied {
		t.Fatalf("ApplyCallback = %d, %v", res, err)
	}
	b := result(t, pool, app.ID, "westbank")
	if b.Status != application.RequestRejected || b.Decision.RejectReason != "insufficient_income" {
		t.Fatalf("westbank = %+v, want rejected", b)
	}
}

func TestCompleteRetryReschedules(t *testing.T) {
	pool := pgtest.NewPool(t)
	q := postgres.NewQueue(pool)
	app := seed(t, pool, "eastbank")
	task := claim(t, q, 1)[0]

	retry := dispatch.Outcome{Kind: dispatch.OutcomeRetry, RetryAfter: time.Hour, Error: "temporary bank error: unexpected status 503"}
	if !complete(t, q, task, retry) {
		t.Fatal("retry was not written")
	}
	if s := result(t, pool, app.ID, "eastbank").Status; s != application.RequestQueued {
		t.Fatalf("status = %s, want queued", s)
	}
	saved := count(t, pool, `SELECT count(*) FROM bank_requests
		WHERE last_error = 'temporary bank error: unexpected status 503' AND locked_until IS NULL`)
	if saved != 1 {
		t.Fatal("last_error is not saved or lease is not released")
	}
	if got := claim(t, q, 1); len(got) != 0 {
		t.Fatal("retry must wait for next_attempt_at")
	}
	exec(t, pool, `UPDATE bank_requests SET next_attempt_at = now()`)
	again := claim(t, q, 1)
	if len(again) != 1 || again[0].Attempts != 2 {
		t.Fatalf("second claim = %+v, want one task with attempt 2", again)
	}
}

func TestCompleteFailed(t *testing.T) {
	pool := pgtest.NewPool(t)
	q := postgres.NewQueue(pool)
	app := seed(t, pool, "northbank")
	failed := dispatch.Outcome{Kind: dispatch.OutcomeFailed, Error: "permanent bank error: unexpected status 400"}
	if !complete(t, q, claim(t, q, 1)[0], failed) {
		t.Fatal("failure was not written")
	}
	b := result(t, pool, app.ID, "northbank")
	if b.Status != application.RequestFailed || b.LastError != "permanent bank error: unexpected status 400" {
		t.Fatalf("northbank = %+v, want failed with error", b)
	}
}

func TestCompleteAfterLostLeaseIsDropped(t *testing.T) {
	pool := pgtest.NewPool(t)
	q := postgres.NewQueue(pool)
	app := seed(t, pool, "northbank")
	stale := claim(t, q, 1)[0]

	exec(t, pool, `UPDATE bank_requests SET locked_until = now() - interval '1 second'`)
	stats, err := q.Sweep(t.Context(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Requeued != 1 {
		t.Fatalf("requeued = %d, want 1", stats.Requeued)
	}
	fresh := claim(t, q, 1)[0]
	if fresh.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", fresh.Attempts)
	}

	decision := dispatch.Outcome{Kind: dispatch.OutcomeDecision, Decision: approved}
	if complete(t, q, stale, decision) {
		t.Fatal("stale owner must not write the result")
	}
	if s := result(t, pool, app.ID, "northbank").Status; s != application.RequestSending {
		t.Fatalf("status = %s, want sending", s)
	}
	if !complete(t, q, fresh, decision) {
		t.Fatal("current owner must write the result")
	}
}

func TestSweepCallbackTimeout(t *testing.T) {
	pool := pgtest.NewPool(t)
	q := postgres.NewQueue(pool)
	app := seed(t, pool, "westbank")
	complete(t, q, claim(t, q, 1)[0], dispatch.Outcome{Kind: dispatch.OutcomeAccepted, ExternalID: "w-1", CallbackTimeout: time.Minute})
	exec(t, pool, `UPDATE bank_requests SET callback_deadline = now() - interval '1 second'`)

	stats, err := q.Sweep(t.Context(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if stats.CallbackTimeouts != 1 {
		t.Fatalf("callback timeouts = %d, want 1", stats.CallbackTimeouts)
	}
	b := result(t, pool, app.ID, "westbank")
	if b.Status != application.RequestFailed || b.LastError != "callback timeout" {
		t.Fatalf("westbank = %+v, want failed with callback timeout", b)
	}
}

func TestSweepFailsExpiredLeaseWithoutAttemptsLeft(t *testing.T) {
	pool := pgtest.NewPool(t)
	q := postgres.NewQueue(pool)
	app := seed(t, pool, "northbank")
	claim(t, q, 1)
	exec(t, pool, `UPDATE bank_requests SET locked_until = now() - interval '1 second'`)

	stats, err := q.Sweep(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if stats.LeaseExpired != 1 || stats.Requeued != 0 {
		t.Fatalf("stats = %+v, want one expired lease", stats)
	}
	b := result(t, pool, app.ID, "northbank")
	if b.Status != application.RequestFailed || b.LastError != "lease expired" {
		t.Fatalf("northbank = %+v, want failed with lease expired", b)
	}
}

func TestCallbackBeforeExternalIDIsUnknown(t *testing.T) {
	pool := pgtest.NewPool(t)
	q := postgres.NewQueue(pool)
	app := seed(t, pool, "westbank")
	claim(t, q, 1)

	res, err := q.ApplyCallback(t.Context(), "westbank", "w-early", approved)
	if err != nil || res != application.CallbackUnknown {
		t.Fatalf("ApplyCallback = %d, %v, want unknown", res, err)
	}
	if s := result(t, pool, app.ID, "westbank").Status; s != application.RequestSending {
		t.Fatalf("status = %s, want sending", s)
	}
}
