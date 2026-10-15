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

// TestDecideBoundaries закрепляет таблицу раздела 6.4 для каждого банка. Пороги взяты из спеки,
// а не из профилей, поэтому тест ловит и неверное число, и неверное сравнение: возраст — включительный
// диапазон, нагрузка — отказ, только если платёж строго больше половины дохода.
func TestDecideBoundaries(t *testing.T) {
	bornAgo := func(years, days int) time.Time {
		return time.Date(testNow.Year()-years, testNow.Month(), testNow.Day()+days, 0, 0, 0, 0, time.UTC)
	}
	banks := []struct {
		name           string
		profile        Profile
		minAge, maxAge int
		pensioner      string // код отказа для пенсионера; пусто — принимается
		minIncome      int64  // копейки
		maxAmount      int64  // копейки
		rateBPS        int
	}{
		{"north", NorthProfile, 21, 70, "", 3_000_000, 300_000_000, 1590},
		{"east", EastProfile, 18, 65, "employment_not_accepted", 5_000_000, 500_000_000, 1490},
		{"west", WestProfile, 18, 75, "", 2_500_000, 200_000_000, 1890},
	}
	for _, b := range banks {
		payment := Annuity(100_000_000, b.rateBPS, 24)
		incomeFirst := "insufficient_income" // пенсионер с доходом ниже минимума
		if b.pensioner != "" {
			incomeFirst = b.pensioner
		}
		tests := []struct {
			name string
			edit func(*LoanRequest)
			want string // пусто — одобрение
		}{
			{"turns min age today", func(r *LoanRequest) { r.BirthDate = bornAgo(b.minAge, 0) }, ""},
			{"turns min age tomorrow", func(r *LoanRequest) { r.BirthDate = bornAgo(b.minAge, 1) }, "age_out_of_range"},
			{"turns max age + 1 tomorrow", func(r *LoanRequest) { r.BirthDate = bornAgo(b.maxAge+1, 1) }, ""},
			{"turns max age + 1 today", func(r *LoanRequest) { r.BirthDate = bornAgo(b.maxAge+1, 0) }, "age_out_of_range"},
			{"pensioner", func(r *LoanRequest) { r.Employment = application.EmploymentPensioner }, b.pensioner},
			{"income at minimum", func(r *LoanRequest) { r.MonthlyIncome, r.Amount = b.minIncome, 3_000_000 }, ""},
			{"income below minimum", func(r *LoanRequest) { r.MonthlyIncome, r.Amount = b.minIncome-1, 3_000_000 }, "insufficient_income"},
			{"amount at limit", func(r *LoanRequest) { r.MonthlyIncome, r.Amount = 1_000_000_000, b.maxAmount }, ""},
			{"amount above limit", func(r *LoanRequest) { r.MonthlyIncome, r.Amount = 1_000_000_000, b.maxAmount+1 }, "amount_exceeds_limit"},
			{"payment exactly half of income", func(r *LoanRequest) { r.MonthlyIncome, r.Amount = 2*payment, 100_000_000 }, ""},
			{"payment above half of income", func(r *LoanRequest) { r.MonthlyIncome, r.Amount = 2*payment-1, 100_000_000 }, "debt_burden"},
			{"employment is checked before income", func(r *LoanRequest) {
				r.Employment, r.MonthlyIncome, r.Amount = application.EmploymentPensioner, b.minIncome-1, 3_000_000
			}, incomeFirst},
			{"income is checked before amount", func(r *LoanRequest) { r.MonthlyIncome, r.Amount = b.minIncome-1, b.maxAmount+1 }, "insufficient_income"},
			{"amount is checked before debt burden", func(r *LoanRequest) { r.MonthlyIncome, r.Amount = b.minIncome, b.maxAmount+1 }, "amount_exceeds_limit"},
		}
		for _, tt := range tests {
			t.Run(b.name+": "+tt.name, func(t *testing.T) {
				req := sampleRequest()
				tt.edit(&req)
				got := b.profile.Decide(req, testNow)
				if got.Approved != (tt.want == "") || got.RejectReason != tt.want {
					t.Fatalf("got %+v, want reject reason %q (empty means approval)", got, tt.want)
				}
				if got.Approved && got.RateBPS != b.rateBPS {
					t.Fatalf("rate = %d bps, want %d", got.RateBPS, b.rateBPS)
				}
			})
		}
	}
}
