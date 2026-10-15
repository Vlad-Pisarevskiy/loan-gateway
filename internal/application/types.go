// Package application — домен заявки на кредит наличными: типы, валидация, сценарии.
package application

import (
	"time"

	"github.com/google/uuid"
)

// EmploymentType — тип занятости заявителя.
type EmploymentType string

const (
	EmploymentEmployed     EmploymentType = "employed"
	EmploymentSelfEmployed EmploymentType = "self_employed"
	EmploymentEntrepreneur EmploymentType = "entrepreneur"
	EmploymentPensioner    EmploymentType = "pensioner"
)

// Input — тело POST /v1/applications. Денежные поля — в копейках.
type Input struct {
	FullName       string         `json:"full_name"`
	BirthDate      string         `json:"birth_date"`
	Phone          string         `json:"phone"`
	MonthlyIncome  int64          `json:"monthly_income"`
	EmploymentType EmploymentType `json:"employment_type"`
	Amount         int64          `json:"amount"`
	TermMonths     int            `json:"term_months"`
}

// Application — провалидированная заявка.
type Application struct {
	ID             uuid.UUID
	FullName       string
	BirthDate      time.Time // полночь UTC
	Phone          string
	MonthlyIncome  int64
	EmploymentType EmploymentType
	Amount         int64
	TermMonths     int
}

// Decision — решение банка. При одобрении заполнены сумма, ставка, срок и платёж,
// при отказе — RejectReason.
type Decision struct {
	Approved       bool
	Amount         int64
	RateBPS        int
	TermMonths     int
	MonthlyPayment int64
	RejectReason   string
}

// RequestStatus — статус запроса в один банк (bank_requests.status).
type RequestStatus string

const (
	RequestQueued          RequestStatus = "queued"
	RequestSending         RequestStatus = "sending"
	RequestWaitingCallback RequestStatus = "waiting_callback"
	RequestApproved        RequestStatus = "approved"
	RequestRejected        RequestStatus = "rejected"
	RequestFailed          RequestStatus = "failed"
)

// Final сообщает, что статус финальный и больше не изменится.
func (s RequestStatus) Final() bool {
	return s == RequestApproved || s == RequestRejected || s == RequestFailed
}

// Status — сводный статус заявки. Не хранится, вычисляется по запросам в банки.
type Status string

const (
	StatusProcessing Status = "processing"
	StatusCompleted  Status = "completed"
)

// BankResult — состояние запроса в один банк.
type BankResult struct {
	BankCode  string
	Status    RequestStatus
	Attempts  int
	Decision  *Decision // для approved и rejected
	LastError string    // для failed
}

// View — заявка с результатами по банкам, отсортированными по BankCode.
type View struct {
	ID         uuid.UUID
	Amount     int64
	TermMonths int
	CreatedAt  time.Time
	Banks      []BankResult
	// Replayed заполняет только Service.Create: заявка уже существовала, вызов — повтор
	// с тем же ключом идемпотентности и тем же телом.
	Replayed bool
}

// Status возвращает completed, если все запросы в банки финальные, иначе processing.
func (v View) Status() Status {
	for _, b := range v.Banks {
		if !b.Status.Final() {
			return StatusProcessing
		}
	}
	return StatusCompleted
}

// CallbackResult — итог применения вебхука банка.
type CallbackResult int

const (
	CallbackApplied   CallbackResult = iota + 1 // решение записано
	CallbackDuplicate                           // запрос уже не ждёт вебхук: повтор или опоздание
	CallbackUnknown                             // запроса с таким external_id нет
)

// CallbackApply — итог применения вебхука.
type CallbackApply struct {
	Result      CallbackResult
	RequestID   uuid.UUID // id запроса в банк; только для CallbackApplied
	TraceParent string    // контекст трейса заявки; только для CallbackApplied, "" — не сохранён
}
