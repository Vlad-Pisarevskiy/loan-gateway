// Package mockbank — заглушки банков-партнёров: правила решений (раздел 6.4 спеки),
// общая механика (задержка, искусственные сбои, идемпотентность) и диалекты банков.
package mockbank

import (
	"math"
	"slices"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
)

// Profile — правила решений одного банка.
type Profile struct {
	MinAge, MaxAge   int
	RejectEmployment []application.EmploymentType
	MinIncome        int64 // копейки
	MaxAmount        int64 // копейки
	RateBPS          int
}

// Профили банков из раздела 6.4 спеки.
var (
	NorthProfile = Profile{MinAge: 21, MaxAge: 70, MinIncome: 3_000_000, MaxAmount: 300_000_000, RateBPS: 1590}
	EastProfile  = Profile{MinAge: 18, MaxAge: 65, RejectEmployment: []application.EmploymentType{application.EmploymentPensioner}, MinIncome: 5_000_000, MaxAmount: 500_000_000, RateBPS: 1490}
	WestProfile  = Profile{MinAge: 18, MaxAge: 75, MinIncome: 2_500_000, MaxAmount: 200_000_000, RateBPS: 1890}
)

// LoanRequest — заявка в терминах заглушки, независимо от диалекта.
type LoanRequest struct {
	BirthDate     time.Time
	MonthlyIncome int64
	Employment    application.EmploymentType
	Amount        int64
	TermMonths    int
}

// Decide применяет проверки по порядку; первая сработавшая даёт отказ.
// Одобряется запрошенная сумма на запрошенный срок.
func (p Profile) Decide(req LoanRequest, now time.Time) application.Decision {
	if age := application.Age(req.BirthDate, now); age < p.MinAge || age > p.MaxAge {
		return reject("age_out_of_range")
	}
	if slices.Contains(p.RejectEmployment, req.Employment) {
		return reject("employment_not_accepted")
	}
	if req.MonthlyIncome < p.MinIncome {
		return reject("insufficient_income")
	}
	if req.Amount > p.MaxAmount {
		return reject("amount_exceeds_limit")
	}
	payment := Annuity(req.Amount, p.RateBPS, req.TermMonths)
	if payment*2 > req.MonthlyIncome {
		return reject("debt_burden")
	}
	return application.Decision{
		Approved:       true,
		Amount:         req.Amount,
		RateBPS:        p.RateBPS,
		TermMonths:     req.TermMonths,
		MonthlyPayment: payment,
	}
}

func reject(reason string) application.Decision {
	return application.Decision{RejectReason: reason}
}

// Annuity — ежемесячный аннуитетный платёж в копейках: P = S·i / (1 − (1+i)^(−n)), i = ставка/12,
// округление до копейки, половина — вверх. Единственное место в проекте, где допустим float.
func Annuity(amount int64, rateBPS, months int) int64 {
	i := float64(rateBPS) / 10000 / 12
	p := float64(amount) * i / (1 - math.Pow(1+i, -float64(months)))
	return int64(math.Floor(p + 0.5))
}
