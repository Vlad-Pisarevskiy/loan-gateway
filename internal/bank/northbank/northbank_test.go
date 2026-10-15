package northbank

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/mockbank"
)

func testApp() application.Application {
	return application.Application{
		ID:             uuid.New(),
		FullName:       "Иванов Иван",
		BirthDate:      time.Date(1990, 5, 17, 0, 0, 0, 0, time.UTC),
		Phone:          "+79991234567",
		MonthlyIncome:  12_000_000,
		EmploymentType: application.EmploymentSelfEmployed,
		Amount:         30_000_000,
		TermMonths:     24,
	}
}

var approved = application.Decision{Approved: true, Amount: 30_000_000, RateBPS: 1590, TermMonths: 24, MonthlyPayment: 1_467_460}

const approvedBody = `{"decision":"APPROVED","offer":{"sum":"300000.00","ratePercent":"15.90","months":24,"payment":"14674.60"}}`

func TestSubmitSendsNorthbankRequest(t *testing.T) {
	reqID := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/loan-requests" || r.Header.Get("X-Request-Id") != reqID.String() {
			t.Errorf("unexpected request %s %s, X-Request-Id %q", r.Method, r.URL.Path, r.Header.Get("X-Request-Id"))
		}
		body, _ := io.ReadAll(r.Body)
		want := `{"client":{"name":"Иванов Иван","birthDate":"1990-05-17","phone":"+79991234567"},"income":"120000.00","employment":"SELF_EMPLOYED","loan":{"sum":"300000.00","months":24}}`
		if string(body) != want {
			t.Errorf("body:\n%s\nwant:\n%s", body, want)
		}
		_, _ = w.Write([]byte(approvedBody))
	}))
	defer srv.Close()

	res, err := New(srv.URL, srv.Client()).Submit(t.Context(), bank.SubmitRequest{RequestID: reqID, Application: testApp()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision == nil || *res.Decision != approved || res.ExternalID != "" {
		t.Fatalf("result = %+v, want synchronous approval", res)
	}
}

func TestSubmitKeepsKopecks(t *testing.T) {
	var got struct {
		Income string `json:"income"`
		Loan   struct {
			Sum string `json:"sum"`
		} `json:"loan"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(approvedBody))
	}))
	defer srv.Close()

	app := testApp()
	app.Amount = 30_000_050
	app.MonthlyIncome = 12_000_001
	if _, err := New(srv.URL, srv.Client()).Submit(t.Context(), bank.SubmitRequest{RequestID: uuid.New(), Application: app}); err != nil {
		t.Fatal(err)
	}
	if got.Loan.Sum != "300000.50" || got.Income != "120000.01" {
		t.Fatalf("sum %q, income %q; want 300000.50 and 120000.01", got.Loan.Sum, got.Income)
	}
}

func TestSubmitDeclined(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"decision":"DECLINED","reason":"debt_burden"}`))
	}))
	defer srv.Close()
	res, err := New(srv.URL, srv.Client()).Submit(t.Context(), bank.SubmitRequest{RequestID: uuid.New(), Application: testApp()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision == nil || res.Decision.Approved || res.Decision.RejectReason != "debt_burden" {
		t.Fatalf("result = %+v, want rejection", res)
	}
}

func TestSubmitErrorClasses(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		permanent bool
	}{
		{"server error", http.StatusServiceUnavailable, `{}`, false},
		{"rate limited", http.StatusTooManyRequests, `{}`, false},
		{"bad request", http.StatusBadRequest, `{}`, true},
		{"malformed json", http.StatusOK, `not json`, true},
		{"unknown decision", http.StatusOK, `{"decision":"MAYBE"}`, true},
		{"approved without offer", http.StatusOK, `{"decision":"APPROVED"}`, true},
		{"bad offer", http.StatusOK, `{"decision":"APPROVED","offer":{"sum":"abc","ratePercent":"15.90","months":24,"payment":"1.00"}}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			_, err := New(srv.URL, srv.Client()).Submit(t.Context(), bank.SubmitRequest{RequestID: uuid.New(), Application: testApp()})
			if err == nil {
				t.Fatal("want error")
			}
			if bank.IsPermanent(err) != tt.permanent {
				t.Fatalf("err = %v, permanent = %v, want %v", err, bank.IsPermanent(err), tt.permanent)
			}
		})
	}
}

func TestSubmitAgainstMockbank(t *testing.T) {
	srv := httptest.NewServer(mockbank.NewNorth(mockbank.Options{}))
	defer srv.Close()
	app := testApp()
	app.EmploymentType = application.EmploymentEmployed
	res, err := New(srv.URL, srv.Client()).Submit(t.Context(), bank.SubmitRequest{RequestID: uuid.New(), Application: app})
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision == nil || *res.Decision != approved {
		t.Fatalf("result = %+v, want %+v", res, approved)
	}
}
