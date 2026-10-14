package e2e

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank/eastbank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/mockbank"
)

func eastbankMock(o mockbank.Options, timeout time.Duration) mockBank {
	return mockBank{
		code:    eastbank.Code,
		mock:    func(string) http.Handler { return mockbank.NewEast(o) },
		adapter: func(url string, c *http.Client) bank.Bank { return eastbank.New(url, c) },
		timeout: timeout,
	}
}

func TestEastbankRecoversAfterRetries(t *testing.T) {
	e := start(t, options{banks: []mockBank{eastbankMock(mockbank.Options{FailFirst: 2}, 0)}})
	got := e.submit(nil).bank(t, eastbank.Code)
	want := offerView{Amount: 30_000_000, RateBPS: 1490, TermMonths: 24, MonthlyPayment: 1_453_175}
	if got.Status != "approved" || got.Attempts != 3 || got.Offer == nil || *got.Offer != want {
		t.Fatalf("eastbank = %+v, want approved on attempt 3", got)
	}
}

func TestEastbankFailsWhenAttemptsExhausted(t *testing.T) {
	e := start(t, options{banks: []mockBank{eastbankMock(mockbank.Options{FailFirst: 10}, 0)}, maxAttempts: 3})
	got := e.submit(nil).bank(t, eastbank.Code)
	if got.Status != "failed" || got.Attempts != 3 || !strings.Contains(got.Error, "503") {
		t.Fatalf("eastbank = %+v, want failed after 3 attempts with 503", got)
	}
}

func TestEastbankTimeoutIsRetried(t *testing.T) {
	slow := eastbankMock(mockbank.Options{Latency: 300 * time.Millisecond}, 50*time.Millisecond)
	e := start(t, options{banks: []mockBank{slow}, maxAttempts: 2})
	got := e.submit(nil).bank(t, eastbank.Code)
	if got.Status != "failed" || got.Attempts != 2 || !strings.Contains(got.Error, "deadline exceeded") {
		t.Fatalf("eastbank = %+v, want failed after 2 timed out attempts", got)
	}
}

func TestEastbankRejectsPensioner(t *testing.T) {
	e := start(t, options{banks: []mockBank{eastbankMock(mockbank.Options{}, 0)}})
	got := e.submit(func(in map[string]any) { in["employment_type"] = "pensioner" }).bank(t, eastbank.Code)
	if got.Status != "rejected" || got.RejectReason != "employment_not_accepted" {
		t.Fatalf("eastbank = %+v, want rejected with employment_not_accepted", got)
	}
}
