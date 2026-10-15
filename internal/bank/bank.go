// Package bank — интеграция с банками-партнёрами: интерфейсы адаптеров, классы ошибок,
// реестр, конвертация денег и подпись вебхуков. Адаптеры живут в подпакетах.
package bank

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
)

// SubmitRequest — заявка для отправки в банк.
type SubmitRequest struct {
	RequestID   uuid.UUID // bank_requests.id — ключ идемпотентности для банка
	Application application.Application
}

// SubmitResult — ответ банка: либо синхронное решение, либо id заявки в банке
// для асинхронного решения через вебхук.
type SubmitResult struct {
	Decision   *application.Decision
	ExternalID string
}

// Bank — адаптер одного банка.
type Bank interface {
	Code() string
	Submit(ctx context.Context, req SubmitRequest) (SubmitResult, error)
}

// CallbackHandler реализуют асинхронные банки: разбор тела вебхука с решением.
type CallbackHandler interface {
	ParseCallback(body []byte) (externalID string, d application.Decision, err error)
}

var (
	// ErrTemporary — сбой, после которого запрос стоит повторить.
	ErrTemporary = errors.New("temporary bank error")
	// ErrPermanent — повтор не поможет, запрос сразу failed.
	ErrPermanent = errors.New("permanent bank error")
)

// IsPermanent сообщает, что ошибка постоянная. Ошибка без класса считается временной.
func IsPermanent(err error) bool {
	return errors.Is(err, ErrPermanent)
}

// StatusError возвращает ошибку нужного класса для неуспешного HTTP-статуса банка:
// 429 и 5xx — временные, остальные — постоянные.
func StatusError(code int) error {
	if code == http.StatusTooManyRequests || code >= 500 {
		return fmt.Errorf("%w: unexpected status %d", ErrTemporary, code)
	}
	return fmt.Errorf("%w: unexpected status %d", ErrPermanent, code)
}
