package mockbank

import (
	"testing"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
)

var testNow = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

func sampleRequest() LoanRequest {
	return LoanRequest{
		BirthDate:     time.Date(1990, 5, 17, 0, 0, 0, 0, time.UTC),
		MonthlyIncome: 12_000_000,
		Employment:    application.EmploymentEmployed,
		Amount:        30_000_000,
		TermMonths:    24,
	}
}

func TestAnnuity(t *testing.T) {
	tests := []struct {
		rateBPS int
		want    int64
	}{
		{1590, 1_467_460},
		{1490, 1_453_175},
		{1890, 1_510_801},
	}
	for _, tt := range tests {
		if got := Annuity(30_000_000, tt.rateBPS, 24); got != tt.want {
			t.Errorf("Annuity(300000 ₽, %d bps, 24) = %d, want %d", tt.rateBPS, got, tt.want)
		}
	}
}

func TestProfilesApproveSampleRequest(t *testing.T) {
	for name, p := range map[string]Profile{"north": NorthProfile, "east": EastProfile, "west": WestProfile} {
		want := application.Decision{
			Approved:       true,
			Amount:         30_000_000,
			RateBPS:        p.RateBPS,
			TermMonths:     24,
			MonthlyPayment: Annuity(30_000_000, p.RateBPS, 24),
		}
		if got := p.Decide(sampleRequest(), testNow); got != want {
			t.Errorf("%s: got %+v, want %+v", name, got, want)
		}
	}
}

func TestDecideRejections(t *testing.T) {
	tests := []struct {
		name    string
		profile Profile
		edit    func(*LoanRequest)
		want    string
	}{
		{"north: 20 years old", NorthProfile, func(r *LoanRequest) { r.BirthDate = time.Date(2006, 1, 1, 0, 0, 0, 0, time.UTC) }, "age_out_of_range"},
		{"east: 66 years old", EastProfile, func(r *LoanRequest) { r.BirthDate = time.Date(1960, 1, 1, 0, 0, 0, 0, time.UTC) }, "age_out_of_range"},
		{"east: pensioner", EastProfile, func(r *LoanRequest) { r.Employment = application.EmploymentPensioner }, "employment_not_accepted"},
		{"north: income below minimum", NorthProfile, func(r *LoanRequest) { r.MonthlyIncome = 2_999_999 }, "insufficient_income"},
		{"west: amount above limit", WestProfile, func(r *LoanRequest) { r.Amount = 200_000_001 }, "amount_exceeds_limit"},
		{"north: payment above half of income", NorthProfile, func(r *LoanRequest) {
			r.MonthlyIncome = 5_000_000
			r.Amount = 100_000_000
		}, "debt_burden"},
		{"east: age is checked before employment", EastProfile, func(r *LoanRequest) {
			r.Employment = application.EmploymentPensioner
			r.BirthDate = time.Date(1956, 1, 1, 0, 0, 0, 0, time.UTC)
		}, "age_out_of_range"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := sampleRequest()
			tt.edit(&req)
			got := tt.profile.Decide(req, testNow)
			if got.Approved || got.RejectReason != tt.want {
				t.Fatalf("got %+v, want rejection %q", got, tt.want)
			}
		})
	}
}
