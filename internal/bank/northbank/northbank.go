// Package northbank — адаптер northbank: синхронный JSON, рубли и проценты строками,
// camelCase (раздел 6.3 спеки).
package northbank

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
)

// Code — код банка в шлюзе.
const Code = "northbank"

type client struct {
	Name      string `json:"name"`
	BirthDate string `json:"birthDate"`
	Phone     string `json:"phone"`
}

type loan struct {
	Sum    string `json:"sum"`
	Months int    `json:"months"`
}

type request struct {
	Client     client `json:"client"`
	Income     string `json:"income"`
	Employment string `json:"employment"`
	Loan       loan   `json:"loan"`
}

type offer struct {
	Sum         string `json:"sum"`
	RatePercent string `json:"ratePercent"`
	Months      int    `json:"months"`
	Payment     string `json:"payment"`
}

type response struct {
	Decision string `json:"decision"`
	Offer    *offer `json:"offer"`
	Reason   string `json:"reason"`
}

// Client — адаптер northbank.
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
		Client:     client{Name: a.FullName, BirthDate: a.BirthDate.Format(time.DateOnly), Phone: a.Phone},
		Income:     bank.FormatRubles(a.MonthlyIncome),
		Employment: strings.ToUpper(string(a.EmploymentType)),
		Loan:       loan{Sum: bank.FormatRubles(a.Amount), Months: a.TermMonths},
	}
	headers := map[string]string{"X-Request-Id": req.RequestID.String()}
	status, data, err := bank.PostJSON(ctx, c.http, c.baseURL+"/api/v1/loan-requests", headers, body)
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
	d, err := resp.decision()
	if err != nil {
		return bank.SubmitResult{}, err
	}
	return bank.SubmitResult{Decision: &d}, nil
}

func (r response) decision() (application.Decision, error) {
	switch r.Decision {
	case "DECLINED":
		return application.Decision{RejectReason: r.Reason}, nil
	case "APPROVED":
		if r.Offer == nil {
			return application.Decision{}, fmt.Errorf("%w: approved without offer", bank.ErrPermanent)
		}
		amount, errAmount := bank.ParseRubles(r.Offer.Sum)
		rate, errRate := bank.ParsePercent(r.Offer.RatePercent)
		payment, errPayment := bank.ParseRubles(r.Offer.Payment)
		if err := errors.Join(errAmount, errRate, errPayment); err != nil {
			return application.Decision{}, fmt.Errorf("%w: bad offer: %w", bank.ErrPermanent, err)
		}
		return application.Decision{
			Approved:       true,
			Amount:         amount,
			RateBPS:        rate,
			TermMonths:     r.Offer.Months,
			MonthlyPayment: payment,
		}, nil
	default:
		return application.Decision{}, fmt.Errorf("%w: unknown decision %q", bank.ErrPermanent, r.Decision)
	}
}
