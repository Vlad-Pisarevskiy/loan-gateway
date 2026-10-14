package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
)

const validBody = `{"full_name":"Иванов Иван","birth_date":"1990-05-17","phone":"+79991234567","monthly_income":12000000,"employment_type":"employed","amount":30000000,"term_months":24}`

func withKey(key string) map[string]string {
	return map[string]string{"Idempotency-Key": key}
}

func newTestHandler(t *testing.T, svc *fakeService) http.Handler {
	return NewHandler(Deps{Applications: svc, DB: fakePinger{}, Log: testLogger(t)})
}

func TestCreateApplicationAccepted(t *testing.T) {
	id := uuid.New()
	var (
		gotKey   string
		gotInput application.Input
	)
	svc := &fakeService{create: func(_ context.Context, key string, in application.Input) (application.View, error) {
		gotKey, gotInput = key, in
		return application.View{ID: id, Banks: []application.BankResult{{Status: application.RequestQueued}}}, nil
	}}
	rec := do(newTestHandler(t, svc), http.MethodPost, "/v1/applications", validBody, withKey("key-1"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content type = %q", ct)
	}
	var resp createResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ID != id || resp.Status != application.StatusProcessing {
		t.Fatalf("response = %+v", resp)
	}
	if gotKey != "key-1" || gotInput.Amount != 30_000_000 || gotInput.FullName != "Иванов Иван" {
		t.Fatalf("service got key %q and input %+v", gotKey, gotInput)
	}
}

func TestCreateApplicationRejectsBadRequests(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		body    string
	}{
		{"missing key", nil, validBody},
		{"key too long", withKey(strings.Repeat("k", 129)), validBody},
		{"non-ascii key", withKey("ключ"), validBody},
		{"malformed json", withKey("k"), `{`},
		{"unknown field", withKey("k"), `{"full_name":"Иван","extra":1}`},
		{"wrong type", withKey("k"), `{"amount":"300000"}`},
		{"trailing data", withKey("k"), validBody + `{}`},
	}
	svc := &fakeService{create: func(context.Context, string, application.Input) (application.View, error) {
		t.Error("service must not be called")
		return application.View{}, nil
	}}
	h := newTestHandler(t, svc)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(h, http.MethodPost, "/v1/applications", tt.body, tt.headers)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("code = %d, want 400; body %s", rec.Code, rec.Body)
			}
			decodeProblem(t, rec)
		})
	}
}

func TestCreateApplicationBodyTooLarge(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"huge field", `{"full_name":"` + strings.Repeat("a", 1<<20) + `"}`},
		{"valid object with huge tail", validBody + strings.Repeat(" ", 1<<20)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(newTestHandler(t, &fakeService{}), http.MethodPost, "/v1/applications", tt.body, withKey("k"))
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("code = %d, want 413", rec.Code)
			}
			decodeProblem(t, rec)
		})
	}
}

func TestUnknownRoutesReturnProblem(t *testing.T) {
	id := uuid.NewString()
	tests := []struct {
		method, path string
		code         int
		allow        string
	}{
		{http.MethodGet, "/v1/nope", http.StatusNotFound, ""},
		{http.MethodGet, "/v1/applications/", http.StatusNotFound, ""},
		{http.MethodPut, "/v1/applications", http.StatusMethodNotAllowed, "POST"},
		{http.MethodDelete, "/v1/applications/" + id, http.StatusMethodNotAllowed, "GET, HEAD"},
		{http.MethodGet, "/v1/webhooks/banks/westbank", http.StatusMethodNotAllowed, "POST"},
		{http.MethodPost, "/healthz", http.StatusMethodNotAllowed, "GET, HEAD"},
		{http.MethodPost, "/readyz", http.StatusMethodNotAllowed, "GET, HEAD"},
	}
	h := newTestHandler(t, &fakeService{})
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			rec := do(h, tt.method, tt.path, "", nil)
			if rec.Code != tt.code {
				t.Fatalf("code = %d, want %d", rec.Code, tt.code)
			}
			if got := rec.Header().Get("Allow"); got != tt.allow {
				t.Fatalf("Allow = %q, want %q", got, tt.allow)
			}
			decodeProblem(t, rec)
		})
	}
}

func TestCreateApplicationServiceErrors(t *testing.T) {
	validation := &application.ValidationError{Fields: []application.FieldError{{Field: "amount", Message: "must be between"}}}
	tests := []struct {
		name string
		err  error
		code int
	}{
		{"validation", validation, http.StatusBadRequest},
		{"conflict", application.ErrIdempotencyConflict, http.StatusConflict},
		{"internal", errors.New("db is down: secret details"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeService{create: func(context.Context, string, application.Input) (application.View, error) {
				return application.View{}, tt.err
			}}
			rec := do(newTestHandler(t, svc), http.MethodPost, "/v1/applications", validBody, withKey("k"))
			if rec.Code != tt.code {
				t.Fatalf("code = %d, want %d", rec.Code, tt.code)
			}
			p := decodeProblem(t, rec)
			if strings.Contains(rec.Body.String(), "secret details") {
				t.Fatal("internal error details leaked to the client")
			}
			if tt.code == http.StatusBadRequest && (len(p.Errors) != 1 || p.Errors[0].Field != "amount") {
				t.Fatalf("errors = %+v, want one for amount", p.Errors)
			}
		})
	}
}

func TestGetApplication(t *testing.T) {
	id := uuid.New()
	view := application.View{
		ID:         id,
		Amount:     30_000_000,
		TermMonths: 24,
		CreatedAt:  time.Date(2026, 10, 13, 15, 0, 0, 0, time.FixedZone("MSK", 3*3600)),
		Banks: []application.BankResult{
			{BankCode: "eastbank", Status: application.RequestRejected, Attempts: 3,
				Decision: &application.Decision{RejectReason: "insufficient_income"}},
			{BankCode: "northbank", Status: application.RequestApproved, Attempts: 1,
				Decision: &application.Decision{Approved: true, Amount: 30_000_000, RateBPS: 1590, TermMonths: 24, MonthlyPayment: 1_467_460}},
			{BankCode: "westbank", Status: application.RequestFailed, Attempts: 1, LastError: "callback timeout"},
		},
	}
	svc := &fakeService{get: func(_ context.Context, got uuid.UUID) (application.View, error) {
		if got != id {
			return application.View{}, application.ErrNotFound
		}
		return view, nil
	}}
	rec := do(newTestHandler(t, svc), http.MethodGet, "/v1/applications/"+id.String(), "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
	}
	want := `{"id":"` + id.String() + `","status":"completed","created_at":"2026-10-13T12:00:00Z","amount":30000000,"term_months":24,"banks":[` +
		`{"bank_code":"eastbank","status":"rejected","attempts":3,"reject_reason":"insufficient_income"},` +
		`{"bank_code":"northbank","status":"approved","attempts":1,"offer":{"amount":30000000,"rate_bps":1590,"term_months":24,"monthly_payment":1467460}},` +
		`{"bank_code":"westbank","status":"failed","attempts":1,"error":"callback timeout"}]}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Fatalf("body:\n%s\nwant:\n%s", got, want)
	}
}

func TestGetApplicationErrors(t *testing.T) {
	svc := &fakeService{get: func(context.Context, uuid.UUID) (application.View, error) {
		return application.View{}, application.ErrNotFound
	}}
	h := newTestHandler(t, svc)
	if rec := do(h, http.MethodGet, "/v1/applications/not-a-uuid", "", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: code = %d, want 400", rec.Code)
	}
	rec := do(h, http.MethodGet, "/v1/applications/"+uuid.NewString(), "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id: code = %d, want 404", rec.Code)
	}
	decodeProblem(t, rec)
}
