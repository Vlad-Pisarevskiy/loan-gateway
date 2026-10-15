package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank/westbank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/mockbank"
)

var westSecret = []byte("e2e-west-secret")

// westbankMock — асинхронная заглушка; skip — не присылать вебхук вовсе.
func westbankMock(t *testing.T, skip bool) mockBank {
	return mockBank{
		code: westbank.Code,
		mock: func(apiURL string) http.Handler {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			return mockbank.NewWest(ctx, mockbank.Options{}, mockbank.CallbackOptions{
				URL:           apiURL + "/v1/webhooks/banks/westbank",
				Secret:        westSecret,
				Delay:         50 * time.Millisecond,
				Skip:          skip,
				Retries:       3,
				RetryInterval: 50 * time.Millisecond,
			})
		},
		adapter: func(url string, c *http.Client) bank.Bank { return westbank.New(url, c) },
		secret:  westSecret,
	}
}

func TestWestbankApprovesViaWebhook(t *testing.T) {
	e := start(t, options{banks: []mockBank{westbankMock(t, false)}})
	got := e.submit(nil).bank(t, westbank.Code)
	want := offerView{Amount: 30_000_000, RateBPS: 1890, TermMonths: 24, MonthlyPayment: 1_510_801}
	if got.Status != "approved" || got.Attempts != 1 || got.Offer == nil || *got.Offer != want {
		t.Fatalf("westbank = %+v, want approved via webhook", got)
	}
}

func TestWestbankDeclinesViaWebhook(t *testing.T) {
	e := start(t, options{banks: []mockBank{westbankMock(t, false)}})
	got := e.submit(func(in map[string]any) { in["monthly_income"] = 2_000_000 }).bank(t, westbank.Code)
	if got.Status != "rejected" || got.RejectReason != "insufficient_income" {
		t.Fatalf("westbank = %+v, want rejected with insufficient_income", got)
	}
}

func TestWestbankCallbackTimeout(t *testing.T) {
	e := start(t, options{banks: []mockBank{westbankMock(t, true)}, callbackTimeout: 300 * time.Millisecond})
	got := e.submit(nil).bank(t, westbank.Code)
	if got.Status != "failed" || got.Error != "callback timeout" {
		t.Fatalf("westbank = %+v, want failed with callback timeout", got)
	}
}
