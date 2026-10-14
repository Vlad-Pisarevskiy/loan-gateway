package eastbank

import (
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

var approved = application.Decision{Approved: true, Amount: 30_000_000, RateBPS: 1490, TermMonths: 24, MonthlyPayment: 1_453_175}

func submit(t *testing.T, url string, app application.Application) (bank.SubmitResult, error) {
	t.Helper()
	return New(url, http.DefaultClient).Submit(t.Context(), bank.SubmitRequest{RequestID: uuid.New(), Application: app})
}

func TestSubmitSendsEastbankRequest(t *testing.T) {
	reqID := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/applications" || r.Header.Get("Idempotency-Key") != reqID.String() {
			t.Errorf("unexpected request %s, Idempotency-Key %q", r.URL.Path, r.Header.Get("Idempotency-Key"))
		}
		body, _ := io.ReadAll(r.Body)
		want := `{"applicant_name":"Иванов Иван","date_of_birth":"1990-05-17","phone_number":"+79991234567","income_kopecks":12000000,"employment_kind":"self_employed","requested_kopecks":30000000,"term":24}`
		if string(body) != want {
			t.Errorf("body:\n%s\nwant:\n%s", body, want)
		}
		_, _ = w.Write([]byte(`{"status":"approved","approved_kopecks":30000000,"rate_bps":1490,"term":24,"monthly_payment_kopecks":1453175}`))
	}))
	defer srv.Close()

	res, err := New(srv.URL, srv.Client()).Submit(t.Context(), bank.SubmitRequest{RequestID: reqID, Application: testApp()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision == nil || *res.Decision != approved {
		t.Fatalf("result = %+v, want %+v", res, approved)
	}
}

func TestSubmitRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"rejected","reject_code":"debt_burden"}`))
	}))
	defer srv.Close()
	res, err := submit(t, srv.URL, testApp())
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
		{"unknown status", http.StatusOK, `{"status":"maybe"}`, true},
		{"incomplete approval", http.StatusOK, `{"status":"approved"}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			_, err := submit(t, srv.URL, testApp())
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
	srv := httptest.NewServer(mockbank.NewEast(mockbank.Options{}))
	defer srv.Close()
	app := testApp()
	app.EmploymentType = application.EmploymentEmployed
	res, err := submit(t, srv.URL, app)
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision == nil || *res.Decision != approved {
		t.Fatalf("result = %+v, want %+v", res, approved)
	}
}
