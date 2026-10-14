package postgres_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/store/postgres"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/store/postgres/pgtest"
)

var bankCodes = []string{"westbank", "northbank"} // нарочно не по алфавиту

func TestCreateStoresApplicationWithQueuedRequests(t *testing.T) {
	pool := pgtest.NewPool(t)
	repo := postgres.NewApplications(pool)
	app := newApp()

	id, stored, err := repo.Create(t.Context(), "key-1", []byte("hash-1"), app, bankCodes)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id != app.ID || string(stored) != "hash-1" {
		t.Fatalf("got %s %q, want %s %q", id, stored, app.ID, "hash-1")
	}
	v, err := repo.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v.ID != app.ID || v.Amount != app.Amount || v.TermMonths != app.TermMonths || v.CreatedAt.IsZero() {
		t.Fatalf("unexpected view %+v", v)
	}
	if len(v.Banks) != 2 || v.Banks[0].BankCode != "northbank" || v.Banks[1].BankCode != "westbank" {
		t.Fatalf("banks = %+v, want northbank and westbank in this order", v.Banks)
	}
	for _, b := range v.Banks {
		if b.Status != application.RequestQueued || b.Attempts != 0 || b.Decision != nil {
			t.Errorf("bank %s: %+v, want a fresh queued request", b.BankCode, b)
		}
	}
	if v.Status() != application.StatusProcessing {
		t.Fatalf("status = %s, want processing", v.Status())
	}
}

func TestCreateWithSameKeyReturnsExisting(t *testing.T) {
	pool := pgtest.NewPool(t)
	repo := postgres.NewApplications(pool)
	first := newApp()
	if _, _, err := repo.Create(t.Context(), "key-1", []byte("hash-1"), first, bankCodes); err != nil {
		t.Fatal(err)
	}

	id, stored, err := repo.Create(t.Context(), "key-1", []byte("hash-2"), newApp(), bankCodes)
	if err != nil {
		t.Fatal(err)
	}
	if id != first.ID || string(stored) != "hash-1" {
		t.Fatalf("got %s %q, want the first application %s %q", id, stored, first.ID, "hash-1")
	}
	if n := count(t, pool, `SELECT count(*) FROM applications`); n != 1 {
		t.Fatalf("applications = %d, want 1", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM bank_requests`); n != 2 {
		t.Fatalf("bank requests = %d, want 2", n)
	}
}

func TestConcurrentCreateWithSameKeyCreatesOneApplication(t *testing.T) {
	pool := pgtest.NewPool(t)
	repo := postgres.NewApplications(pool)
	const n = 10
	ids := make([]uuid.UUID, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			ids[i], _, errs[i] = repo.Create(t.Context(), "same-key", []byte("h"), newApp(), bankCodes)
		})
	}
	wg.Wait()
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("Create %d: %v", i, errs[i])
		}
		if ids[i] != ids[0] {
			t.Fatalf("Create %d returned %s, want %s", i, ids[i], ids[0])
		}
	}
	if got := count(t, pool, `SELECT count(*) FROM applications`); got != 1 {
		t.Fatalf("applications = %d, want 1", got)
	}
	if got := count(t, pool, `SELECT count(*) FROM bank_requests`); got != 2 {
		t.Fatalf("bank requests = %d, want 2", got)
	}
}

func TestGetUnknownReturnsNotFound(t *testing.T) {
	repo := postgres.NewApplications(pgtest.NewPool(t))
	if _, err := repo.Get(t.Context(), uuid.Must(uuid.NewV7())); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
