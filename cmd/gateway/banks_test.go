package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/config"
)

// asyncBank — фейковый асинхронный банк для проверки требований к секрету вебхука.
type asyncBank struct{}

func (asyncBank) Code() string { return "asyncbank" }

func (asyncBank) Submit(context.Context, bank.SubmitRequest) (bank.SubmitResult, error) {
	return bank.SubmitResult{}, nil
}

func (asyncBank) ParseCallback([]byte) (string, application.Decision, error) {
	return "", application.Decision{}, nil
}

func withAsyncBank(t *testing.T) {
	t.Helper()
	adapters["asyncbank"] = func(string, *http.Client) bank.Bank { return asyncBank{} }
	t.Cleanup(func() { delete(adapters, "asyncbank") })
}

func TestBankCodesAreSorted(t *testing.T) {
	codes := bankCodes()
	if !slices.IsSorted(codes) || !slices.Contains(codes, "northbank") {
		t.Fatalf("codes = %v", codes)
	}
}

func TestBuildBanks(t *testing.T) {
	cfg := config.Gateway{Banks: []config.Bank{{Code: "northbank", URL: "http://northbank:8090", Timeout: 2 * time.Second}}}
	reg, hooks, err := buildBanks(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(reg.Codes(), []string{"northbank"}) || len(hooks) != 0 {
		t.Fatalf("codes = %v, hooks = %v", reg.Codes(), hooks)
	}
	if got := bankTimeouts(cfg)["northbank"]; got != 2*time.Second {
		t.Fatalf("timeout = %s, want 2s", got)
	}
}

func TestBuildBanksRequiresAtLeastOneBank(t *testing.T) {
	if _, _, err := buildBanks(config.Gateway{}); err == nil {
		t.Fatal("want error without banks")
	}
}

func TestBuildBanksRequiresWebhookSecretForAsyncBank(t *testing.T) {
	withAsyncBank(t)
	cfg := config.Gateway{Banks: []config.Bank{{Code: "asyncbank", URL: "http://async"}}}
	if _, _, err := buildBanks(cfg); err == nil || !strings.Contains(err.Error(), "BANK_ASYNCBANK_WEBHOOK_SECRET") {
		t.Fatalf("err = %v, want missing secret error", err)
	}
	cfg.Banks[0].WebhookSecret = "s"
	_, hooks, err := buildBanks(cfg)
	if err != nil || string(hooks["asyncbank"].Secret) != "s" {
		t.Fatalf("hooks = %v, err = %v", hooks, err)
	}
}

func TestBanksDoNotFollowRedirects(t *testing.T) {
	var followed atomic.Bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed.Store(true) }))
	defer elsewhere.Close()
	srv := httptest.NewServer(http.RedirectHandler(elsewhere.URL, http.StatusTemporaryRedirect))
	defer srv.Close()

	// Через реестр из buildBanks: проверяется клиент, который получают адаптеры.
	reg, _, err := buildBanks(config.Gateway{Banks: []config.Bank{{Code: "northbank", URL: srv.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := reg.Get("northbank")
	app := application.Application{ID: uuid.New(), FullName: "Иванов Иван", BirthDate: time.Date(1990, 5, 17, 0, 0, 0, 0, time.UTC),
		Phone: "+79991234567", MonthlyIncome: 12_000_000, EmploymentType: application.EmploymentEmployed, Amount: 30_000_000, TermMonths: 24}
	_, err = b.Submit(t.Context(), bank.SubmitRequest{RequestID: uuid.New(), Application: app})
	if !bank.IsPermanent(err) {
		t.Fatalf("redirect: err = %v, want permanent bank error", err)
	}
	if followed.Load() {
		t.Fatal("client re-sent the applicant's data to the redirect target")
	}
}
