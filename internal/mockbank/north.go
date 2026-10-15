package mockbank

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
)

// Диалект north: синхронный JSON, рубли и проценты строками, camelCase (раздел 6.3 спеки).

type northClient struct {
	Name      string `json:"name"`
	BirthDate string `json:"birthDate"`
	Phone     string `json:"phone"`
}

type northLoan struct {
	Sum    string `json:"sum"`
	Months int    `json:"months"`
}

type northRequest struct {
	Client     northClient `json:"client"`
	Income     string      `json:"income"`
	Employment string      `json:"employment"`
	Loan       northLoan   `json:"loan"`
}

type northOffer struct {
	Sum         string `json:"sum"`
	RatePercent string `json:"ratePercent"`
	Months      int    `json:"months"`
	Payment     string `json:"payment"`
}

type northResponse struct {
	Decision string      `json:"decision"`
	Offer    *northOffer `json:"offer,omitempty"`
	Reason   string      `json:"reason,omitempty"`
}

// NewNorth — заглушка northbank: POST /api/v1/loan-requests, ключ идемпотентности — X-Request-Id.
func NewNorth(opts Options) http.Handler {
	s := newServer(opts)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/loan-requests", func(w http.ResponseWriter, r *http.Request) {
		s.handle(w, r, r.Header.Get("X-Request-Id"), func(body []byte) (int, any) {
			var req northRequest
			if err := json.Unmarshal(body, &req); err != nil {
				return http.StatusBadRequest, errorResponse{Error: "invalid json"}
			}
			lr, err := req.toLoanRequest()
			if err != nil {
				return http.StatusBadRequest, errorResponse{Error: err.Error()}
			}
			return http.StatusOK, northResponseFrom(NorthProfile.Decide(lr, s.opts.Now()))
		})
	})
	return mux
}

func (r northRequest) toLoanRequest() (LoanRequest, error) {
	birth, err := time.Parse(time.DateOnly, r.Client.BirthDate)
	if err != nil {
		return LoanRequest{}, errors.New("client.birthDate must be YYYY-MM-DD")
	}
	income, err := bank.ParseRubles(r.Income)
	if err != nil {
		return LoanRequest{}, fmt.Errorf("income: %w", err)
	}
	sum, err := bank.ParseRubles(r.Loan.Sum)
	if err != nil {
		return LoanRequest{}, fmt.Errorf("loan.sum: %w", err)
	}
	if r.Loan.Months <= 0 {
		return LoanRequest{}, errors.New("loan.months must be positive")
	}
	return LoanRequest{
		BirthDate:     birth,
		MonthlyIncome: income,
		Employment:    application.EmploymentType(strings.ToLower(r.Employment)),
		Amount:        sum,
		TermMonths:    r.Loan.Months,
	}, nil
}

func northResponseFrom(d application.Decision) northResponse {
	if !d.Approved {
		return northResponse{Decision: "DECLINED", Reason: d.RejectReason}
	}
	return northResponse{Decision: "APPROVED", Offer: &northOffer{
		Sum:         bank.FormatRubles(d.Amount),
		RatePercent: bank.FormatPercent(d.RateBPS),
		Months:      d.TermMonths,
		Payment:     bank.FormatRubles(d.MonthlyPayment),
	}}
}
