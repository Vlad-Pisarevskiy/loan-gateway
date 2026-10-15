// Package westbank — адаптер westbank: асинхронный — заявка принимается с 202, решение приходит
// вебхуком (разделы 5.3 и 6.3 спеки).
package westbank

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
)

// Code — код банка в шлюзе.
const Code = "westbank"

type request struct {
	FIO          string `json:"fio"`
	DOB          string `json:"dob"`
	Tel          string `json:"tel"`
	Salary       string `json:"salary"`
	Job          string `json:"job"`
	Amount       string `json:"amount"`
	PeriodMonths int    `json:"period_months"`
}

type accepted struct {
	ApplicationID string `json:"application_id"`
}

type offer struct {
	Amount       string `json:"amount"`
	Rate         string `json:"rate"`
	PeriodMonths int    `json:"period_months"`
	Monthly      string `json:"monthly"`
}

type callback struct {
	ApplicationID string `json:"application_id"`
	Result        string `json:"result"`
	Offer         *offer `json:"offer"`
	Reason        string `json:"reason"`
}

// Client — адаптер westbank; реализует bank.Bank и bank.CallbackHandler.
type Client struct {
	baseURL string
	http    *http.Client
}

// New создаёт адаптер; baseURL — адрес банка без завершающего слеша.
func New(baseURL string, httpClient *http.Client) *Client {
	return &Client{baseURL: baseURL, http: httpClient}
}

// Code возвращает код банка.
func (c *Client) Code() string { return Code }

// Submit отправляет заявку. Решение придёт вебхуком, поэтому возвращается только ExternalID.
func (c *Client) Submit(ctx context.Context, req bank.SubmitRequest) (bank.SubmitResult, error) {
	a := req.Application
	body := request{
		FIO:          a.FullName,
		DOB:          a.BirthDate.Format(time.DateOnly),
		Tel:          a.Phone,
		Salary:       bank.FormatRubles(a.MonthlyIncome),
		Job:          string(a.EmploymentType),
		Amount:       bank.FormatRubles(a.Amount),
		PeriodMonths: a.TermMonths,
	}
	headers := map[string]string{"X-Idempotency-Key": req.RequestID.String()}
	status, data, err := bank.PostJSON(ctx, c.http, c.baseURL+"/loans/apply", headers, body)
	if err != nil {
		return bank.SubmitResult{}, err
	}
	if status != http.StatusAccepted {
		return bank.SubmitResult{}, bank.StatusError(status)
	}
	var resp accepted
	if err := json.Unmarshal(data, &resp); err != nil {
		return bank.SubmitResult{}, fmt.Errorf("%w: decode response: %w", bank.ErrPermanent, err)
	}
	if resp.ApplicationID == "" {
		return bank.SubmitResult{}, fmt.Errorf("%w: empty application_id", bank.ErrPermanent)
	}
	return bank.SubmitResult{ExternalID: resp.ApplicationID}, nil
}

// ParseCallback разбирает вебхук westbank с решением.
func (c *Client) ParseCallback(body []byte) (string, application.Decision, error) {
	var cb callback
	if err := json.Unmarshal(body, &cb); err != nil {
		return "", application.Decision{}, fmt.Errorf("decode callback: %w", err)
	}
	if cb.ApplicationID == "" {
		return "", application.Decision{}, errors.New("callback without application_id")
	}
	switch cb.Result {
	case "declined":
		return cb.ApplicationID, application.Decision{RejectReason: cb.Reason}, nil
	case "approved":
		if cb.Offer == nil {
			return "", application.Decision{}, errors.New("approved callback without offer")
		}
		amount, errAmount := bank.ParseRubles(cb.Offer.Amount)
		rate, errRate := bank.ParsePercent(cb.Offer.Rate)
		monthly, errMonthly := bank.ParseRubles(cb.Offer.Monthly)
		if err := errors.Join(errAmount, errRate, errMonthly); err != nil {
			return "", application.Decision{}, fmt.Errorf("bad offer: %w", err)
		}
		return cb.ApplicationID, application.Decision{
			Approved:       true,
			Amount:         amount,
			RateBPS:        rate,
			TermMonths:     cb.Offer.PeriodMonths,
			MonthlyPayment: monthly,
		}, nil
	default:
		return "", application.Decision{}, fmt.Errorf("unknown callback result %q", cb.Result)
	}
}
