package mockbank

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
)

// Диалект west: асинхронный — 202 с id заявки, решение вебхуком с HMAC-подписью (раздел 6.3 спеки).

// CallbackOptions — доставка вебхуков диалекта west.
type CallbackOptions struct {
	URL           string        // куда слать вебхук
	Secret        []byte        // секрет HMAC
	Delay         time.Duration // задержка перед первой отправкой
	Skip          bool          // не слать вебхук вовсе
	Retries       int           // повторов при не-2xx или сетевой ошибке (по спеке 3)
	RetryInterval time.Duration // интервал между повторами (по спеке 2 с)
	Client        *http.Client  // nil — http.DefaultClient
	Log           *slog.Logger  // nil — без логов
}

type westRequest struct {
	FIO          string `json:"fio"`
	DOB          string `json:"dob"`
	Tel          string `json:"tel"`
	Salary       string `json:"salary"`
	Job          string `json:"job"`
	Amount       string `json:"amount"`
	PeriodMonths int    `json:"period_months"`
}

type westAccepted struct {
	ApplicationID string `json:"application_id"`
}

type westOffer struct {
	Amount       string `json:"amount"`
	Rate         string `json:"rate"`
	PeriodMonths int    `json:"period_months"`
	Monthly      string `json:"monthly"`
}

type westCallback struct {
	ApplicationID string     `json:"application_id"`
	Result        string     `json:"result"` // approved | declined
	Offer         *westOffer `json:"offer,omitempty"`
	Reason        string     `json:"reason,omitempty"`
}

type west struct {
	ctx context.Context
	srv *server
	cb  CallbackOptions
}

// NewWest — заглушка westbank: POST /loans/apply с ключом X-Idempotency-Key отвечает 202
// с application_id, решение уходит вебхуком на cb.URL. Отправка прекращается при отмене ctx.
func NewWest(ctx context.Context, opts Options, cb CallbackOptions) http.Handler {
	if cb.Client == nil {
		cb.Client = http.DefaultClient
	}
	if cb.Log == nil {
		cb.Log = slog.New(slog.DiscardHandler)
	}
	w := &west{ctx: ctx, srv: newServer(opts), cb: cb}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /loans/apply", w.apply)
	return mux
}

func (w *west) apply(rw http.ResponseWriter, r *http.Request) {
	w.srv.handle(rw, r, r.Header.Get("X-Idempotency-Key"), func(body []byte) (int, any) {
		var req westRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return http.StatusBadRequest, errorResponse{Error: "invalid json"}
		}
		lr, err := req.toLoanRequest()
		if err != nil {
			return http.StatusBadRequest, errorResponse{Error: err.Error()}
		}
		id := "w-" + uuid.NewString()
		callback := westCallbackFrom(id, WestProfile.Decide(lr, w.srv.opts.Now()))
		if !w.cb.Skip {
			go w.deliver(callback)
		}
		return http.StatusAccepted, westAccepted{ApplicationID: id}
	})
}

// deliver отправляет вебхук после задержки и повторяет его при неудаче.
func (w *west) deliver(c westCallback) {
	body, _ := json.Marshal(c) // westCallback состоит из строк и чисел
	if !w.sleep(w.cb.Delay) {
		return
	}
	for attempt := 0; attempt <= w.cb.Retries; attempt++ {
		if attempt > 0 && !w.sleep(w.cb.RetryInterval) {
			return
		}
		err := w.send(body)
		if err == nil {
			return
		}
		w.cb.Log.Warn("callback delivery failed", "application_id", c.ApplicationID, "attempt", attempt+1, "error", err)
	}
}

// sleep ждёт d; false — ctx отменён.
func (w *west) sleep(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-w.ctx.Done():
		return false
	}
}

func (w *west) send(body []byte) error {
	ts := strconv.FormatInt(w.srv.opts.Now().Unix(), 10)
	req, err := http.NewRequestWithContext(w.ctx, http.MethodPost, w.cb.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(bank.HeaderWebhookTimestamp, ts)
	req.Header.Set(bank.HeaderWebhookSignature, bank.SignWebhook(w.cb.Secret, ts, body))
	resp, err := w.cb.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return nil
}

func (r westRequest) toLoanRequest() (LoanRequest, error) {
	birth, err := time.Parse(time.DateOnly, r.DOB)
	if err != nil {
		return LoanRequest{}, errors.New("dob must be YYYY-MM-DD")
	}
	salary, err := bank.ParseRubles(r.Salary)
	if err != nil {
		return LoanRequest{}, fmt.Errorf("salary: %w", err)
	}
	amount, err := bank.ParseRubles(r.Amount)
	if err != nil {
		return LoanRequest{}, fmt.Errorf("amount: %w", err)
	}
	if r.PeriodMonths <= 0 {
		return LoanRequest{}, errors.New("period_months must be positive")
	}
	return LoanRequest{
		BirthDate:     birth,
		MonthlyIncome: salary,
		Employment:    application.EmploymentType(r.Job),
		Amount:        amount,
		TermMonths:    r.PeriodMonths,
	}, nil
}

func westCallbackFrom(id string, d application.Decision) westCallback {
	if !d.Approved {
		return westCallback{ApplicationID: id, Result: "declined", Reason: d.RejectReason}
	}
	return westCallback{ApplicationID: id, Result: "approved", Offer: &westOffer{
		Amount:       bank.FormatRubles(d.Amount),
		Rate:         bank.FormatPercent(d.RateBPS),
		PeriodMonths: d.TermMonths,
		Monthly:      bank.FormatRubles(d.MonthlyPayment),
	}}
}
