package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type storedApp struct {
	id   uuid.UUID
	hash []byte
}

// fakeRepo — хранилище заявок в памяти.
type fakeRepo struct {
	byKey map[string]storedApp
	views map[uuid.UUID]View
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{byKey: map[string]storedApp{}, views: map[uuid.UUID]View{}}
}

func (f *fakeRepo) Create(_ context.Context, key string, hash []byte, app Application, bankCodes []string) (uuid.UUID, []byte, error) {
	if s, ok := f.byKey[key]; ok {
		return s.id, s.hash, nil
	}
	f.byKey[key] = storedApp{id: app.ID, hash: hash}
	banks := make([]BankResult, len(bankCodes))
	for i, code := range bankCodes {
		banks[i] = BankResult{BankCode: code, Status: RequestQueued}
	}
	f.views[app.ID] = View{ID: app.ID, Amount: app.Amount, TermMonths: app.TermMonths, Banks: banks}
	return app.ID, hash, nil
}

func (f *fakeRepo) Get(_ context.Context, id uuid.UUID) (View, error) {
	v, ok := f.views[id]
	if !ok {
		return View{}, ErrNotFound
	}
	return v, nil
}

func newTestService(repo Repository) *Service {
	return NewService(repo, []string{"eastbank", "northbank"}, func() time.Time { return testNow })
}

func TestServiceCreate(t *testing.T) {
	v, err := newTestService(newFakeRepo()).Create(t.Context(), "key-1", validInput())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if v.ID == uuid.Nil {
		t.Fatal("id is not set")
	}
	if v.Status() != StatusProcessing || len(v.Banks) != 2 {
		t.Fatalf("got status %s with %d banks, want processing with 2", v.Status(), len(v.Banks))
	}
}

func TestServiceCreateIsIdempotent(t *testing.T) {
	svc := newTestService(newFakeRepo())
	first, err := svc.Create(t.Context(), "key-1", validInput())
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Create(t.Context(), "key-1", validInput())
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("repeat returned %s, want %s", second.ID, first.ID)
	}
	if first.Replayed || !second.Replayed {
		t.Fatalf("Replayed: first %v, second %v; want false then true", first.Replayed, second.Replayed)
	}
}

func TestServiceCreateConflict(t *testing.T) {
	svc := newTestService(newFakeRepo())
	if _, err := svc.Create(t.Context(), "key-1", validInput()); err != nil {
		t.Fatal(err)
	}
	other := validInput()
	other.Amount += 100
	_, err := svc.Create(t.Context(), "key-1", other)
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("err = %v, want ErrIdempotencyConflict", err)
	}
}

func TestServiceCreateValidatesBeforeSaving(t *testing.T) {
	repo := newFakeRepo()
	in := validInput()
	in.TermMonths = 0
	_, err := newTestService(repo).Create(t.Context(), "key-1", in)
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want *ValidationError, got %v", err)
	}
	if len(repo.byKey) != 0 {
		t.Fatal("invalid application was saved")
	}
}

func TestServiceGetNotFound(t *testing.T) {
	_, err := newTestService(newFakeRepo()).Get(t.Context(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
