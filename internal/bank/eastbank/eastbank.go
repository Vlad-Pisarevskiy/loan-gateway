// Package eastbank — адаптер eastbank: синхронный JSON, копейки и базисные пункты,
// snake_case (раздел 6.3 спеки).
package eastbank

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
)

// Code — код банка в шлюзе.
const Code = "eastbank"

type request struct {
	ApplicantName    string `json:"applicant_name"`
	DateOfBirth      string `json:"date_of_birth"`
	PhoneNumber      string `json:"phone_number"`
	IncomeKopecks    int64  `json:"income_kopecks"`
	EmploymentKind   string `json:"employment_kind"`
	RequestedKopecks int64  `json:"requested_kopecks"`
	Term             int    `json:"term"`
}

type response struct {
	Status                string `json:"status"`
	ApprovedKopecks       int64  `json:"approved_kopecks"`
	RateBPS               int    `json:"rate_bps"`
	Term                  int    `json:"term"`
	MonthlyPaymentKopecks int64  `json:"monthly_payment_kopecks"`
	RejectCode            string `json:"reject_code"`
}

// Client — адаптер eastbank.
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

// Submit отправляет заявку и возвращает синхронное решение.
func (c *Client) Submit(ctx context.Context, req bank.SubmitRequest) (bank.SubmitResult, error) {
	a := req.Application
	body := request{
		ApplicantName:    a.FullName,
		DateOfBirth:      a.BirthDate.Format(time.DateOnly),
		PhoneNumber:      a.Phone,
		IncomeKopecks:    a.MonthlyIncome,
		EmploymentKind:   string(a.EmploymentType),
		RequestedKopecks: a.Amount,
		Term:             a.TermMonths,
	}
	headers := map[string]string{"Idempotency-Key": req.RequestID.String()}
	status, data, err := bank.PostJSON(ctx, c.http, c.baseURL+"/v2/applications", headers, body)
	if err != nil {
		return bank.SubmitResult{}, err
	}
	if status != http.StatusOK {
		return bank.SubmitResult{}, bank.StatusError(status)
	}
	var resp response
	if err := json.Unmarshal(data, &resp); err != nil {
		return bank.SubmitResult{}, fmt.Errorf("%w: decode response: %w", bank.ErrPermanent, err)
	}
	var d application.Decision
	switch resp.Status {
	case "rejected":
		d = application.Decision{RejectReason: resp.RejectCode}
	case "approved":
		if resp.ApprovedKopecks <= 0 || resp.RateBPS <= 0 || resp.Term <= 0 || resp.MonthlyPaymentKopecks <= 0 {
			return bank.SubmitResult{}, fmt.Errorf("%w: incomplete approval", bank.ErrPermanent)
		}
		d = application.Decision{
			Approved:       true,
			Amount:         resp.ApprovedKopecks,
			RateBPS:        resp.RateBPS,
			TermMonths:     resp.Term,
			MonthlyPayment: resp.MonthlyPaymentKopecks,
		}
	default:
		return bank.SubmitResult{}, fmt.Errorf("%w: unknown status %q", bank.ErrPermanent, resp.Status)
	}
	return bank.SubmitResult{Decision: &d}, nil
}
