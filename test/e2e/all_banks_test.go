package e2e

import (
	"testing"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank/eastbank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank/northbank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank/westbank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/mockbank"
)

func TestAllBanksDecide(t *testing.T) {
	e := start(t, options{banks: []mockBank{
		northbankMock(mockbank.Options{}),
		eastbankMock(mockbank.Options{FailFirst: 1}, 0),
		westbankMock(t, false),
	}})
	v := e.submit(nil)
	if len(v.Banks) != 3 {
		t.Fatalf("banks = %+v, want 3", v.Banks)
	}
	payments := map[string]int64{northbank.Code: 1_467_460, eastbank.Code: 1_453_175, westbank.Code: 1_510_801}
	for code, payment := range payments {
		got := v.bank(t, code)
		if got.Status != "approved" || got.Offer == nil || got.Offer.MonthlyPayment != payment {
			t.Errorf("%s = %+v, want approved with payment %d", code, got, payment)
		}
	}
}
