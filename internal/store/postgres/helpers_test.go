package postgres_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
)

// newApp возвращает валидную заявку с новым id.
func newApp() application.Application {
	return application.Application{
		ID:             uuid.Must(uuid.NewV7()),
		FullName:       "Иванов Иван Иванович",
		BirthDate:      time.Date(1990, 5, 17, 0, 0, 0, 0, time.UTC),
		Phone:          "+79991234567",
		MonthlyIncome:  12_000_000,
		EmploymentType: application.EmploymentEmployed,
		Amount:         30_000_000,
		TermMonths:     24,
	}
}

// count выполняет запрос вида SELECT count(*) и возвращает число.
func count(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// exec выполняет SQL для подготовки состояния в тесте.
func exec(t *testing.T, pool *pgxpool.Pool, query string, args ...any) { //nolint:unused // нужен тестам очереди (Task 6)
	t.Helper()
	if _, err := pool.Exec(t.Context(), query, args...); err != nil {
		t.Fatalf("exec: %v", err)
	}
}
