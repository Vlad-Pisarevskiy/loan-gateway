package e2e

import (
	"net/http"
	"testing"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank/northbank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/mockbank"
)

func northbankMock(o mockbank.Options) mockBank {
	return mockBank{
		code:    northbank.Code,
		mock:    func(string) http.Handler { return mockbank.NewNorth(o) },
		adapter: func(url string, c *http.Client) bank.Bank { return northbank.New(url, c) },
	}
}

func TestNorthbankApproves(t *testing.T) {
	e := start(t, options{banks: []mockBank{northbankMock(mockbank.Options{})}})
	got := e.submit(nil).bank(t, northbank.Code)
	want := offerView{Amount: 30_000_000, RateBPS: 1590, TermMonths: 24, MonthlyPayment: 1_467_460}
	if got.Status != "approved" || got.Attempts != 1 || got.Offer == nil || *got.Offer != want {
		t.Fatalf("northbank = %+v, want approved with %+v", got, want)
	}
}

func TestNorthbankRejects(t *testing.T) {
	e := start(t, options{banks: []mockBank{northbankMock(mockbank.Options{})}})
	got := e.submit(func(in map[string]any) { in["monthly_income"] = 2_000_000 }).bank(t, northbank.Code)
	if got.Status != "rejected" || got.RejectReason != "insufficient_income" {
		t.Fatalf("northbank = %+v, want rejected with insufficient_income", got)
	}
}

func TestRepeatedPostReturnsSameApplication(t *testing.T) {
	e := start(t, options{banks: []mockBank{northbankMock(mockbank.Options{})}})
	code1, first := e.post("repeat-key", sampleInput())
	code2, second := e.post("repeat-key", sampleInput())
	if code1 != http.StatusAccepted || code2 != http.StatusAccepted || first.ID != second.ID {
		t.Fatalf("got %d %s and %d %s, want the same application twice", code1, first.ID, code2, second.ID)
	}
	var n int
	err := e.pool.QueryRow(t.Context(), `SELECT count(*) FROM bank_requests WHERE application_id = $1`, first.ID).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("bank requests = %d, want 1", n)
	}
}

func TestSameKeyWithDifferentBodyConflicts(t *testing.T) {
	e := start(t, options{banks: []mockBank{northbankMock(mockbank.Options{})}})
	if code, _ := e.post("conflict-key", sampleInput()); code != http.StatusAccepted {
		t.Fatalf("first POST returned %d", code)
	}
	other := sampleInput()
	other["amount"] = 40_000_000
	if code, _ := e.post("conflict-key", other); code != http.StatusConflict {
		t.Fatalf("second POST returned %d, want 409", code)
	}
}
