package mockbank

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const northSample = `{"client":{"name":"Иванов Иван","birthDate":"1990-05-17","phone":"+79991234567"},"income":"120000.00","employment":"EMPLOYED","loan":{"sum":"300000.00","months":24}}`

func postNorth(h http.Handler, requestID, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/loan-requests", strings.NewReader(body))
	if requestID != "" {
		req.Header.Set("X-Request-Id", requestID)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func newTestNorth() http.Handler {
	return NewNorth(Options{Now: func() time.Time { return testNow }})
}

func TestNorthApproves(t *testing.T) {
	rec := postNorth(newTestNorth(), "r-1", northSample)
	want := `{"decision":"APPROVED","offer":{"sum":"300000.00","ratePercent":"15.90","months":24,"payment":"14674.60"}}`
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("got %d %s\nwant 200 %s", rec.Code, rec.Body, want)
	}
}

func TestNorthDeclines(t *testing.T) {
	body := strings.Replace(northSample, `"income":"120000.00"`, `"income":"20000.00"`, 1)
	rec := postNorth(newTestNorth(), "r-1", body)
	want := `{"decision":"DECLINED","reason":"insufficient_income"}`
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("got %d %s\nwant 200 %s", rec.Code, rec.Body, want)
	}
}

func TestNorthRejectsBadRequests(t *testing.T) {
	tests := []struct {
		name, requestID, body string
	}{
		{"missing request id", "", northSample},
		{"malformed json", "r-1", `{`},
		{"bad birth date", "r-1", strings.Replace(northSample, "1990-05-17", "17.05.1990", 1)},
		{"bad sum", "r-1", strings.Replace(northSample, `"sum":"300000.00"`, `"sum":"abc"`, 1)},
		{"zero months", "r-1", strings.Replace(northSample, `"months":24`, `"months":0`, 1)},
	}
	for _, tt := range tests {
		if rec := postNorth(newTestNorth(), tt.requestID, tt.body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: code = %d, want 400", tt.name, rec.Code)
		}
	}
}
