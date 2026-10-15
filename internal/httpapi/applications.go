package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
)

type createResponse struct {
	ID     uuid.UUID          `json:"id"`
	Status application.Status `json:"status"`
}

type offerResponse struct {
	Amount         int64 `json:"amount"`
	RateBPS        int   `json:"rate_bps"`
	TermMonths     int   `json:"term_months"`
	MonthlyPayment int64 `json:"monthly_payment"`
}

type bankResponse struct {
	BankCode     string                    `json:"bank_code"`
	Status       application.RequestStatus `json:"status"`
	Attempts     int                       `json:"attempts"`
	Offer        *offerResponse            `json:"offer,omitempty"`
	RejectReason string                    `json:"reject_reason,omitempty"`
	Error        string                    `json:"error,omitempty"`
}

type applicationResponse struct {
	ID         uuid.UUID          `json:"id"`
	Status     application.Status `json:"status"`
	CreatedAt  time.Time          `json:"created_at"`
	Amount     int64              `json:"amount"`
	TermMonths int                `json:"term_months"`
	Banks      []bankResponse     `json:"banks"`
}

// createApplication — POST /v1/applications (раздел 5.1 спеки).
func (a *api) createApplication(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	if !validIdempotencyKey(key) {
		writeProblem(w, http.StatusBadRequest, "Invalid Idempotency-Key",
			"header Idempotency-Key is required: 1-128 printable ASCII characters")
		return
	}
	in, status, err := decodeInput(w, r)
	if err != nil {
		writeProblem(w, status, "Invalid request body", err.Error())
		return
	}
	v, err := a.Applications.Create(r.Context(), key, in)
	var verr *application.ValidationError
	switch {
	case errors.As(err, &verr):
		writeProblem(w, http.StatusBadRequest, "Validation failed", "request body has invalid fields", verr.Fields...)
	case errors.Is(err, application.ErrIdempotencyConflict):
		writeProblem(w, http.StatusConflict, "Idempotency key conflict", err.Error())
	case err != nil:
		a.Log.Error("create application failed", "error", err)
		writeProblem(w, http.StatusInternalServerError, "Internal error", "")
	default:
		writeJSON(w, http.StatusAccepted, createResponse{ID: v.ID, Status: v.Status()})
	}
}

// getApplication — GET /v1/applications/{id} (раздел 5.2 спеки).
func (a *api) getApplication(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid id", "id must be a UUID")
		return
	}
	v, err := a.Applications.Get(r.Context(), id)
	switch {
	case errors.Is(err, application.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "Not found", "application not found")
	case err != nil:
		a.Log.Error("get application failed", "application_id", id, "error", err)
		writeProblem(w, http.StatusInternalServerError, "Internal error", "")
	default:
		writeJSON(w, http.StatusOK, toApplicationResponse(v))
	}
}

// decodeInput читает тело: не больше maxBodyBytes, ровно один JSON-объект, без неизвестных полей.
// Возвращает HTTP-статус для ошибки.
func decodeInput(w http.ResponseWriter, r *http.Request) (application.Input, int, error) {
	body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	in, err := decodeObject(body)
	if err == nil {
		return in, 0, nil
	}
	// Ошибка разбора бывает раньше, чем исчерпан лимит: дочитываем тело, чтобы любое тело больше
	// лимита давало 413, а не 400 (раздел 5.1 спеки). После превышения MaxBytesReader снова вернёт ту же ошибку.
	if _, rest := io.Copy(io.Discard, body); tooLarge(rest) {
		return in, http.StatusRequestEntityTooLarge, fmt.Errorf("body exceeds %d bytes", maxBodyBytes)
	}
	return in, http.StatusBadRequest, err
}

// decodeObject читает ровно один JSON-объект без неизвестных полей.
func decodeObject(r io.Reader) (application.Input, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var in application.Input
	if err := dec.Decode(&in); err != nil {
		return in, fmt.Errorf("malformed JSON: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return in, errors.New("body must contain a single JSON object")
	}
	return in, nil
}

func tooLarge(err error) bool {
	var e *http.MaxBytesError
	return errors.As(err, &e)
}

func validIdempotencyKey(k string) bool {
	if len(k) < 1 || len(k) > 128 {
		return false
	}
	for i := 0; i < len(k); i++ {
		if k[i] < 0x20 || k[i] > 0x7e {
			return false
		}
	}
	return true
}

func toApplicationResponse(v application.View) applicationResponse {
	resp := applicationResponse{
		ID:         v.ID,
		Status:     v.Status(),
		CreatedAt:  v.CreatedAt.UTC(),
		Amount:     v.Amount,
		TermMonths: v.TermMonths,
		Banks:      make([]bankResponse, 0, len(v.Banks)),
	}
	for _, b := range v.Banks {
		br := bankResponse{BankCode: b.BankCode, Status: b.Status, Attempts: b.Attempts}
		switch {
		case b.Status == application.RequestApproved && b.Decision != nil:
			d := b.Decision
			br.Offer = &offerResponse{Amount: d.Amount, RateBPS: d.RateBPS, TermMonths: d.TermMonths, MonthlyPayment: d.MonthlyPayment}
		case b.Status == application.RequestRejected && b.Decision != nil:
			br.RejectReason = b.Decision.RejectReason
		case b.Status == application.RequestFailed:
			br.Error = b.LastError
		}
		resp.Banks = append(resp.Banks, br)
	}
	return resp
}
