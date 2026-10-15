// Package dispatch — воркер шлюза: забирает запросы в банки из очереди, вызывает адаптеры,
// ретраит с backoff и возвращает в очередь зависшие задачи.
package dispatch

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
)

// Task — захваченный воркером запрос в банк вместе с заявкой.
type Task struct {
	RequestID   uuid.UUID
	BankCode    string
	Attempts    int    // номер текущей попытки, уже увеличенный при захвате
	TraceParent string // контекст трейса заявки (W3C traceparent); "" — нет
	Application application.Application
}

// OutcomeKind — вид результата обработки задачи.
type OutcomeKind int

const (
	OutcomeDecision OutcomeKind = iota + 1 // банк вернул решение
	OutcomeAccepted                        // банк принял заявку, решение придёт вебхуком
	OutcomeRetry                           // временная ошибка, повторить позже
	OutcomeFailed                          // финальная ошибка
)

func (k OutcomeKind) String() string {
	switch k {
	case OutcomeDecision:
		return "decision"
	case OutcomeAccepted:
		return "accepted"
	case OutcomeRetry:
		return "retry"
	case OutcomeFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Outcome — результат обработки задачи, который воркер записывает в очередь.
type Outcome struct {
	Kind            OutcomeKind
	Decision        application.Decision // для OutcomeDecision
	ExternalID      string               // для OutcomeAccepted
	CallbackTimeout time.Duration        // для OutcomeAccepted: сколько ждать вебхук
	RetryAfter      time.Duration        // для OutcomeRetry
	Error           string               // для OutcomeRetry и OutcomeFailed
}

// SweepStats — сколько задач изменил чистильщик за проход.
type SweepStats struct {
	CallbackTimeouts int64 // waiting_callback → failed
	Requeued         int64 // sending с истёкшей арендой → queued
	LeaseExpired     int64 // sending с истёкшей арендой и исчерпанными попытками → failed
}

// Queue — очередь запросов в банки; реализация — store/postgres.Queue.
type Queue interface {
	// Claim атомарно забирает до limit готовых задач и выдаёт на них аренду lease.
	Claim(ctx context.Context, limit int, lease time.Duration) ([]Task, error)
	// Complete записывает результат; false — аренду перехватили, результат не записан.
	Complete(ctx context.Context, t Task, o Outcome) (bool, error)
	// Sweep закрывает просроченные ожидания вебхуков и возвращает задачи с истёкшей арендой.
	Sweep(ctx context.Context, maxAttempts int) (SweepStats, error)
}
