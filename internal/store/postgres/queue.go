package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/dispatch"
)

// Queue — очередь запросов в банки поверх таблицы bank_requests; реализует dispatch.Queue.
type Queue struct {
	pool *pgxpool.Pool
}

// NewQueue создаёт очередь.
func NewQueue(pool *pgxpool.Pool) *Queue {
	return &Queue{pool: pool}
}

const claimSQL = `
WITH picked AS (
    SELECT id FROM bank_requests
    WHERE status = 'queued' AND next_attempt_at <= now()
    ORDER BY next_attempt_at
    LIMIT $1
    FOR UPDATE SKIP LOCKED
)
UPDATE bank_requests AS br
SET status = 'sending',
    attempts = br.attempts + 1,
    locked_until = now() + $2::bigint * interval '1 millisecond',
    updated_at = now()
FROM picked, applications AS a
WHERE br.id = picked.id AND a.id = br.application_id
RETURNING br.id, br.bank_code, br.attempts,
          a.id, a.full_name, a.birth_date, a.phone, a.monthly_income, a.employment_type, a.amount, a.term_months`

// Claim атомарно переводит до limit готовых задач в sending с арендой lease.
func (q *Queue) Claim(ctx context.Context, limit int, lease time.Duration) ([]dispatch.Task, error) {
	rows, err := q.pool.Query(ctx, claimSQL, limit, lease.Milliseconds())
	if err != nil {
		return nil, fmt.Errorf("claim: %w", err)
	}
	defer rows.Close()
	var tasks []dispatch.Task
	for rows.Next() {
		var (
			t          dispatch.Task
			employment string
		)
		a := &t.Application
		if err := rows.Scan(&t.RequestID, &t.BankCode, &t.Attempts,
			&a.ID, &a.FullName, &a.BirthDate, &a.Phone, &a.MonthlyIncome, &employment, &a.Amount, &a.TermMonths); err != nil {
			return nil, fmt.Errorf("scan claimed task: %w", err)
		}
		a.EmploymentType = application.EmploymentType(employment)
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read claimed tasks: %w", err)
	}
	return tasks, nil
}

// ownerGuard — защита владения: результат пишет только держатель текущей аренды.
const ownerGuard = ` WHERE id = $1 AND status = 'sending' AND attempts = $2`

// Complete записывает результат обработки задачи. false — аренду перехватили.
func (q *Queue) Complete(ctx context.Context, t dispatch.Task, o dispatch.Outcome) (bool, error) {
	var query string
	args := []any{t.RequestID, t.Attempts}
	switch o.Kind {
	case dispatch.OutcomeDecision:
		if o.Decision.Approved {
			query = `UPDATE bank_requests SET status = 'approved', approved_amount = $3, rate_bps = $4,
				approved_term = $5, monthly_payment = $6, locked_until = NULL, updated_at = now()` + ownerGuard
			args = append(args, o.Decision.Amount, o.Decision.RateBPS, o.Decision.TermMonths, o.Decision.MonthlyPayment)
		} else {
			query = `UPDATE bank_requests SET status = 'rejected', reject_reason = $3,
				locked_until = NULL, updated_at = now()` + ownerGuard
			args = append(args, o.Decision.RejectReason)
		}
	case dispatch.OutcomeAccepted:
		query = `UPDATE bank_requests SET status = 'waiting_callback', external_id = $3,
			callback_deadline = now() + $4::bigint * interval '1 millisecond',
			locked_until = NULL, updated_at = now()` + ownerGuard
		args = append(args, o.ExternalID, o.CallbackTimeout.Milliseconds())
	case dispatch.OutcomeRetry:
		query = `UPDATE bank_requests SET status = 'queued',
			next_attempt_at = now() + $3::bigint * interval '1 millisecond',
			last_error = $4, locked_until = NULL, updated_at = now()` + ownerGuard
		args = append(args, o.RetryAfter.Milliseconds(), o.Error)
	case dispatch.OutcomeFailed:
		query = `UPDATE bank_requests SET status = 'failed', last_error = $3,
			locked_until = NULL, updated_at = now()` + ownerGuard
		args = append(args, o.Error)
	default:
		return false, fmt.Errorf("unknown outcome kind %d", o.Kind)
	}
	tag, err := q.pool.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("complete %s: %w", o.Kind, err)
	}
	return tag.RowsAffected() == 1, nil
}

// Sweep закрывает просроченные ожидания вебхуков и возвращает в очередь задачи
// с истёкшей арендой; если попытки исчерпаны — переводит их в failed.
func (q *Queue) Sweep(ctx context.Context, maxAttempts int) (dispatch.SweepStats, error) {
	var s dispatch.SweepStats
	tag, err := q.pool.Exec(ctx, `
		UPDATE bank_requests SET status = 'failed', last_error = 'callback timeout', updated_at = now()
		WHERE status = 'waiting_callback' AND callback_deadline < now()`)
	if err != nil {
		return s, fmt.Errorf("sweep callback timeouts: %w", err)
	}
	s.CallbackTimeouts = tag.RowsAffected()

	tag, err = q.pool.Exec(ctx, `
		UPDATE bank_requests SET status = 'queued', next_attempt_at = now(), locked_until = NULL, updated_at = now()
		WHERE status = 'sending' AND locked_until < now() AND attempts < $1`, maxAttempts)
	if err != nil {
		return s, fmt.Errorf("sweep expired leases: %w", err)
	}
	s.Requeued = tag.RowsAffected()

	tag, err = q.pool.Exec(ctx, `
		UPDATE bank_requests SET status = 'failed', last_error = 'lease expired', locked_until = NULL, updated_at = now()
		WHERE status = 'sending' AND locked_until < now() AND attempts >= $1`, maxAttempts)
	if err != nil {
		return s, fmt.Errorf("sweep exhausted leases: %w", err)
	}
	s.LeaseExpired = tag.RowsAffected()
	return s, nil
}

// ApplyCallback записывает решение из вебхука, если запрос ещё ждёт его.
// Обновление и проверка существования — один SQL-оператор: оба видят один снимок данных.
// Иначе вебхук, пришедший раньше сохранения external_id, мог бы получить Duplicate
// вместо Unknown, и банк не повторил бы его.
func (q *Queue) ApplyCallback(ctx context.Context, bankCode, externalID string, d application.Decision) (application.CallbackResult, error) {
	const (
		guard = ` WHERE bank_code = $1 AND external_id = $2 AND status = 'waiting_callback'`
		tail  = ` RETURNING 1)
		SELECT EXISTS (SELECT 1 FROM upd),
		       EXISTS (SELECT 1 FROM bank_requests WHERE bank_code = $1 AND external_id = $2)`
	)
	query := `WITH upd AS (UPDATE bank_requests SET status = 'rejected', reject_reason = $3, updated_at = now()` + guard + tail
	args := []any{bankCode, externalID, d.RejectReason}
	if d.Approved {
		query = `WITH upd AS (UPDATE bank_requests SET status = 'approved', approved_amount = $3, rate_bps = $4,
			approved_term = $5, monthly_payment = $6, updated_at = now()` + guard + tail
		args = []any{bankCode, externalID, d.Amount, d.RateBPS, d.TermMonths, d.MonthlyPayment}
	}
	var updated, exists bool
	if err := q.pool.QueryRow(ctx, query, args...).Scan(&updated, &exists); err != nil {
		return 0, fmt.Errorf("apply callback: %w", err)
	}
	switch {
	case updated:
		return application.CallbackApplied, nil
	case exists:
		return application.CallbackDuplicate, nil
	default:
		return application.CallbackUnknown, nil
	}
}
