package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"
)

// Applications — реализация application.Repository.
type Applications struct {
	pool *pgxpool.Pool
}

// NewApplications создаёт репозиторий заявок.
func NewApplications(pool *pgxpool.Pool) *Applications {
	return &Applications{pool: pool}
}

// Create в одной транзакции сохраняет заявку и по запросу queued на каждый банк.
// Повтор ключа решает уникальный индекс: ON CONFLICT DO NOTHING, затем чтение существующей записи.
func (r *Applications) Create(ctx context.Context, key string, hash []byte, app application.Application, bankCodes []string) (uuid.UUID, []byte, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // после Commit — no-op

	tag, err := tx.Exec(ctx, `
		INSERT INTO applications (id, idempotency_key, request_hash, full_name, birth_date, phone,
		                          monthly_income, employment_type, amount, term_months)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (idempotency_key) DO NOTHING`,
		app.ID, key, hash, app.FullName, app.BirthDate, app.Phone,
		app.MonthlyIncome, string(app.EmploymentType), app.Amount, app.TermMonths)
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("insert application: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var id uuid.UUID
		var stored []byte
		err := tx.QueryRow(ctx, `SELECT id, request_hash FROM applications WHERE idempotency_key = $1`, key).Scan(&id, &stored)
		if err != nil {
			return uuid.Nil, nil, fmt.Errorf("load existing application: %w", err)
		}
		return id, stored, nil
	}
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
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, nil, fmt.Errorf("commit: %w", err)
	}
	return app.ID, hash, nil
}

// Get возвращает заявку с результатами по банкам или application.ErrNotFound.
func (r *Applications) Get(ctx context.Context, id uuid.UUID) (application.View, error) {
	v := application.View{ID: id}
	err := r.pool.QueryRow(ctx, `SELECT amount, term_months, created_at FROM applications WHERE id = $1`, id).
		Scan(&v.Amount, &v.TermMonths, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.View{}, application.ErrNotFound
	}
	if err != nil {
		return application.View{}, fmt.Errorf("load application: %w", err)
	}

	rows, err := r.pool.Query(ctx, `
		SELECT bank_code, status, attempts, approved_amount, rate_bps, approved_term,
		       monthly_payment, reject_reason, last_error
		FROM bank_requests
		WHERE application_id = $1
		ORDER BY bank_code`, id)
	if err != nil {
		return application.View{}, fmt.Errorf("load bank requests: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			b                       application.BankResult
			status                  string
			amount, payment         *int64
			rate, term              *int
			rejectReason, lastError *string
		)
		if err := rows.Scan(&b.BankCode, &status, &b.Attempts, &amount, &rate, &term, &payment, &rejectReason, &lastError); err != nil {
			return application.View{}, fmt.Errorf("scan bank request: %w", err)
		}
		b.Status = application.RequestStatus(status)
		switch b.Status {
		case application.RequestApproved:
			if amount == nil || rate == nil || term == nil || payment == nil {
				return application.View{}, fmt.Errorf("approved bank request %s has no offer", b.BankCode)
			}
			b.Decision = &application.Decision{Approved: true, Amount: *amount, RateBPS: *rate, TermMonths: *term, MonthlyPayment: *payment}
		case application.RequestRejected:
			b.Decision = &application.Decision{RejectReason: deref(rejectReason)}
		case application.RequestFailed:
			b.LastError = deref(lastError)
		}
		v.Banks = append(v.Banks, b)
	}
	if err := rows.Err(); err != nil {
		return application.View{}, fmt.Errorf("read bank requests: %w", err)
	}
	return v, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
