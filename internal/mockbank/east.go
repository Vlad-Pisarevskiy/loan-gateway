package mockbank

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
)

// Диалект east: синхронный, копейки и базисные пункты, snake_case (раздел 6.3 спеки).

type eastRequest struct {
	ApplicantName    string `json:"applicant_name"`
	DateOfBirth      string `json:"date_of_birth"`
	PhoneNumber      string `json:"phone_number"`
	IncomeKopecks    int64  `json:"income_kopecks"`
	EmploymentKind   string `json:"employment_kind"`
	RequestedKopecks int64  `json:"requested_kopecks"`
	Term             int    `json:"term"`
}

type eastResponse struct {
	Status                string `json:"status"`
	ApprovedKopecks       int64  `json:"approved_kopecks,omitempty"`
	RateBPS               int    `json:"rate_bps,omitempty"`
	Term                  int    `json:"term,omitempty"`
	MonthlyPaymentKopecks int64  `json:"monthly_payment_kopecks,omitempty"`
	RejectCode            string `json:"reject_code,omitempty"`
}

// NewEast — заглушка eastbank: POST /v2/applications, ключ идемпотентности — Idempotency-Key.
func NewEast(opts Options) http.Handler {
	s := newServer(opts)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v2/applications", func(w http.ResponseWriter, r *http.Request) {
		s.handle(w, r, r.Header.Get("Idempotency-Key"), func(body []byte) (int, any) {
			var req eastRequest
			if err := json.Unmarshal(body, &req); err != nil {
				return http.StatusBadRequest, errorResponse{Error: "invalid json"}
			}
			lr, err := req.toLoanRequest()
			if err != nil {
				return http.StatusBadRequest, errorResponse{Error: err.Error()}
			}
			return http.StatusOK, eastResponseFrom(EastProfile.Decide(lr, s.opts.Now()))
		})
	})
	return mux
}

func (r eastRequest) toLoanRequest() (LoanRequest, error) {
	birth, err := time.Parse(time.DateOnly, r.DateOfBirth)
	if err != nil {
		return LoanRequest{}, errors.New("date_of_birth must be YYYY-MM-DD")
	}
	if r.IncomeKopecks <= 0 || r.RequestedKopecks <= 0 || r.Term <= 0 {
		return LoanRequest{}, errors.New("income_kopecks, requested_kopecks and term must be positive")
	}
	return LoanRequest{
		BirthDate:     birth,
		MonthlyIncome: r.IncomeKopecks,
		Employment:    application.EmploymentType(r.EmploymentKind),
		Amount:        r.RequestedKopecks,
		TermMonths:    r.Term,
	}, nil
}

func eastResponseFrom(d application.Decision) eastResponse {
	if !d.Approved {
		return eastResponse{Status: "rejected", RejectCode: d.RejectReason}
	}
	return eastResponse{
		Status:                "approved",
		ApprovedKopecks:       d.Amount,
		RateBPS:               d.RateBPS,
		Term:                  d.TermMonths,
		MonthlyPaymentKopecks: d.MonthlyPayment,
	}
}
