package application

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound            = errors.New("application not found")
	ErrIdempotencyConflict = errors.New("idempotency key already used with a different request")
)

// Repository — хранилище заявок; реализация — store/postgres.Applications.
type Repository interface {
	// Create в одной транзакции сохраняет заявку и по запросу в статусе queued на каждый банк.
	// Если ключ идемпотентности уже занят, ничего не пишет и возвращает id и хеш сохранённой заявки.
	Create(ctx context.Context, key string, hash []byte, app Application, bankCodes []string) (id uuid.UUID, storedHash []byte, err error)
	// Get возвращает заявку с результатами по банкам или ErrNotFound.
	Get(ctx context.Context, id uuid.UUID) (View, error)
}

// Service — сценарии приёма и чтения заявок.
type Service struct {
	repo      Repository
	bankCodes []string
	now       func() time.Time
}

// NewService создаёт сервис; bankCodes — банки, в которые уходит каждая новая заявка.
func NewService(repo Repository, bankCodes []string, now func() time.Time) *Service {
	return &Service{repo: repo, bankCodes: bankCodes, now: now}
}

// Create валидирует вход и идемпотентно создаёт заявку. Повтор с тем же ключом и телом
// возвращает исходную заявку, с тем же ключом и другим телом — ErrIdempotencyConflict.
func (s *Service) Create(ctx context.Context, key string, in Input) (View, error) {
	app, err := Validate(in, s.now())
	if err != nil {
		return View{}, err
	}
	app.ID, err = uuid.NewV7()
	if err != nil {
		return View{}, fmt.Errorf("generate id: %w", err)
	}
	hash := RequestHash(in)
	id, stored, err := s.repo.Create(ctx, key, hash, app, s.bankCodes)
	if err != nil {
		return View{}, fmt.Errorf("create application: %w", err)
	}
	if !bytes.Equal(stored, hash) {
		return View{}, ErrIdempotencyConflict
	}
	return s.repo.Get(ctx, id)
}

// Get возвращает заявку или ErrNotFound.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (View, error) {
	return s.repo.Get(ctx, id)
}
