package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
)

var (
	webhookSecret = []byte("west-secret")
	webhookNow    = time.Unix(1_760_000_000, 0)
)

type callbackCall struct {
	bankCode, externalID string
	decision             application.Decision
}

type fakeCallbacks struct {
	result application.CallbackResult
	err    error
	calls  []callbackCall
}

func (f *fakeCallbacks) ApplyCallback(_ context.Context, bankCode, externalID string, d application.Decision) (application.CallbackResult, error) {
	f.calls = append(f.calls, callbackCall{bankCode: bankCode, externalID: externalID, decision: d})
	return f.result, f.err
}

// testCallbackHandler разбирает тело вида {"id":"...","approved":true,"reason":"..."}.
type testCallbackHandler struct{}

func (testCallbackHandler) ParseCallback(body []byte) (string, application.Decision, error) {
	var p struct {
		ID       string `json:"id"`
		Approved bool   `json:"approved"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(body, &p); err != nil || p.ID == "" {
		return "", application.Decision{}, errors.New("bad callback")
	}
	if p.Approved {
		return p.ID, application.Decision{Approved: true, Amount: 100, RateBPS: 1890, TermMonths: 12, MonthlyPayment: 10}, nil
	}
	return p.ID, application.Decision{RejectReason: p.Reason}, nil
}

func newWebhookHandler(t *testing.T, cb *fakeCallbacks) http.Handler {
	return NewHandler(Deps{
		Applications:   &fakeService{},
		Callbacks:      cb,
		DB:             fakePinger{},
		WebhookBanks:   map[string]WebhookBank{"westbank": {Handler: testCallbackHandler{}, Secret: webhookSecret}},
		WebhookMaxSkew: 5 * time.Minute,
		Log:            testLogger(t),
		Now:            func() time.Time { return webhookNow },
	})
}

func signedAt(at time.Time, body string) map[string]string {
	ts := strconv.FormatInt(at.Unix(), 10)
	return map[string]string{
		bank.HeaderWebhookTimestamp: ts,
		bank.HeaderWebhookSignature: bank.SignWebhook(webhookSecret, ts, []byte(body)),
	}
}

const westPath = "/v1/webhooks/banks/westbank"

func TestWebhookAppliesDecision(t *testing.T) {
	cb := &fakeCallbacks{result: application.CallbackApplied}
	body := `{"id":"w-1","approved":true}`
	rec := do(newWebhookHandler(t, cb), http.MethodPost, westPath, body, signedAt(webhookNow, body))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
	}
	if len(cb.calls) != 1 {
		t.Fatalf("ApplyCallback calls = %d, want 1", len(cb.calls))
	}
	call := cb.calls[0]
	if call.bankCode != "westbank" || call.externalID != "w-1" || !call.decision.Approved {
		t.Fatalf("call = %+v", call)
	}
}

func TestWebhookResults(t *testing.T) {
	tests := []struct {
		name   string
		result application.CallbackResult
		err    error
		code   int
	}{
		{"duplicate or late is acknowledged", application.CallbackDuplicate, nil, http.StatusOK},
		{"unknown request", application.CallbackUnknown, nil, http.StatusNotFound},
		{"store error", 0, errors.New("db down"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb := &fakeCallbacks{result: tt.result, err: tt.err}
			body := `{"id":"w-1","reason":"debt_burden"}`
			rec := do(newWebhookHandler(t, cb), http.MethodPost, westPath, body, signedAt(webhookNow, body))
			if rec.Code != tt.code {
				t.Fatalf("code = %d, want %d", rec.Code, tt.code)
			}
		})
	}
}

func TestWebhookRejectsBadSignatures(t *testing.T) {
	body := `{"id":"w-1","approved":true}`
	tests := []struct {
		name    string
		headers map[string]string
	}{
		{"no headers", nil},
		{"signature of another body", signedAt(webhookNow, `{"id":"w-2","approved":true}`)},
		{"stale timestamp", signedAt(webhookNow.Add(-10*time.Minute), body)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb := &fakeCallbacks{result: application.CallbackApplied}
			rec := do(newWebhookHandler(t, cb), http.MethodPost, westPath, body, tt.headers)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("code = %d, want 401", rec.Code)
			}
			decodeProblem(t, rec)
			if len(cb.calls) != 0 {
				t.Fatal("unsigned webhook must not reach the store")
			}
		})
	}
}

func TestWebhookUnknownBank(t *testing.T) {
	body := `{"id":"w-1","approved":true}`
	rec := do(newWebhookHandler(t, &fakeCallbacks{}), http.MethodPost, "/v1/webhooks/banks/northbank", body, signedAt(webhookNow, body))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
}

func TestWebhookBadBody(t *testing.T) {
	cb := &fakeCallbacks{result: application.CallbackApplied}
	body := `not json`
	rec := do(newWebhookHandler(t, cb), http.MethodPost, westPath, body, signedAt(webhookNow, body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
	if len(cb.calls) != 0 {
		t.Fatal("unparsable webhook must not reach the store")
	}
}
