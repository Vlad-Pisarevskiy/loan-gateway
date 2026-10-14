package westbank

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
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
		EmploymentType: application.EmploymentEmployed,
		Amount:         30_000_000,
		TermMonths:     24,
	}
}

var approved = application.Decision{Approved: true, Amount: 30_000_000, RateBPS: 1890, TermMonths: 24, MonthlyPayment: 1_510_801}

func TestSubmitSendsWestbankRequest(t *testing.T) {
	reqID := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/loans/apply" || r.Header.Get("X-Idempotency-Key") != reqID.String() {
			t.Errorf("unexpected request %s, X-Idempotency-Key %q", r.URL.Path, r.Header.Get("X-Idempotency-Key"))
		}
		body, _ := io.ReadAll(r.Body)
		want := `{"fio":"Иванов Иван","dob":"1990-05-17","tel":"+79991234567","salary":"120000.00","job":"employed","amount":"300000.00","period_months":24}`
		if string(body) != want {
			t.Errorf("body:\n%s\nwant:\n%s", body, want)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"application_id":"w-42"}`))
	}))
	defer srv.Close()

	res, err := New(srv.URL, srv.Client()).Submit(t.Context(), bank.SubmitRequest{RequestID: reqID, Application: testApp()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision != nil || res.ExternalID != "w-42" {
		t.Fatalf("result = %+v, want external id w-42 without decision", res)
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
		{"unexpected 200", http.StatusOK, `{"application_id":"w-1"}`, true},
		{"malformed json", http.StatusAccepted, `not json`, true},
		{"empty application id", http.StatusAccepted, `{}`, true},
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

func TestParseCallback(t *testing.T) {
	c := New("http://unused", http.DefaultClient)
	id, d, err := c.ParseCallback([]byte(`{"application_id":"w-1","result":"approved","offer":{"amount":"300000.00","rate":"18.90","period_months":24,"monthly":"15108.01"}}`))
	if err != nil || id != "w-1" || d != approved {
		t.Fatalf("approved: got %s %+v %v", id, d, err)
	}
	id, d, err = c.ParseCallback([]byte(`{"application_id":"w-2","result":"declined","reason":"debt_burden"}`))
	if err != nil || id != "w-2" || d != (application.Decision{RejectReason: "debt_burden"}) {
		t.Fatalf("declined: got %s %+v %v", id, d, err)
	}
	for _, bad := range []string{
		`not json`,
		`{"result":"approved"}`,
		`{"application_id":"w-1","result":"approved"}`,
		`{"application_id":"w-1","result":"maybe"}`,
		`{"application_id":"w-1","result":"approved","offer":{"amount":"abc","rate":"18.90","period_months":24,"monthly":"1.00"}}`,
	} {
		if _, _, err := c.ParseCallback([]byte(bad)); err == nil {
			t.Errorf("ParseCallback(%s) must fail", bad)
		}
	}
}

func TestAgainstMockbank(t *testing.T) {
	var (
		mu       sync.Mutex
		callback []byte
	)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		callback = body
		mu.Unlock()
	}))
	defer receiver.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mock := httptest.NewServer(mockbank.NewWest(ctx, mockbank.Options{}, mockbank.CallbackOptions{
		URL: receiver.URL, Secret: []byte("s"), Delay: 10 * time.Millisecond,
	}))
	defer mock.Close()

	c := New(mock.URL, mock.Client())
	res, err := c.Submit(t.Context(), bank.SubmitRequest{RequestID: uuid.New(), Application: testApp()})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		got := callback
		mu.Unlock()
		if got != nil {
			id, d, err := c.ParseCallback(got)
			if err != nil || id != res.ExternalID || d != approved {
				t.Fatalf("callback parsed as %s %+v %v; want %s %+v", id, d, err, res.ExternalID, approved)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no callback from mockbank")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
