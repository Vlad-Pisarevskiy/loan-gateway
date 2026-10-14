package mockbank

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const eastSample = `{"applicant_name":"Иванов Иван","date_of_birth":"1990-05-17","phone_number":"+79991234567","income_kopecks":12000000,"employment_kind":"employed","requested_kopecks":30000000,"term":24}`

func postEast(h http.Handler, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v2/applications", strings.NewReader(body))
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func newTestEast() http.Handler {
	return NewEast(Options{Now: func() time.Time { return testNow }})
}

func TestEastApproves(t *testing.T) {
	rec := postEast(newTestEast(), "k-1", eastSample)
	want := `{"status":"approved","approved_kopecks":30000000,"rate_bps":1490,"term":24,"monthly_payment_kopecks":1453175}`
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("got %d %s\nwant 200 %s", rec.Code, rec.Body, want)
	}
}

func TestEastRejectsPensioner(t *testing.T) {
	body := strings.Replace(eastSample, `"employment_kind":"employed"`, `"employment_kind":"pensioner"`, 1)
	rec := postEast(newTestEast(), "k-1", body)
	want := `{"status":"rejected","reject_code":"employment_not_accepted"}`
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("got %d %s\nwant 200 %s", rec.Code, rec.Body, want)
	}
}

func TestEastRejectsBadRequests(t *testing.T) {
	tests := []struct {
		name, key, body string
	}{
		{"missing idempotency key", "", eastSample},
		{"malformed json", "k-1", `{`},
		{"bad birth date", "k-1", strings.Replace(eastSample, "1990-05-17", "17.05.1990", 1)},
		{"zero term", "k-1", strings.Replace(eastSample, `"term":24`, `"term":0`, 1)},
	}
	for _, tt := range tests {
		if rec := postEast(newTestEast(), tt.key, tt.body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: code = %d, want 400", tt.name, rec.Code)
		}
	}
}
