package httpapi

import (
	"errors"
	"net/http"
	"testing"
)

func TestHealth(t *testing.T) {
	up := NewHandler(Deps{Applications: &fakeService{}, DB: fakePinger{}, Log: testLogger(t)})
	down := NewHandler(Deps{Applications: &fakeService{}, DB: fakePinger{err: errors.New("down")}, Log: testLogger(t)})
	tests := []struct {
		name string
		h    http.Handler
		path string
		code int
	}{
		{"liveness", up, "/healthz", http.StatusOK},
		{"liveness ignores database", down, "/healthz", http.StatusOK},
		{"ready", up, "/readyz", http.StatusOK},
		{"not ready without database", down, "/readyz", http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		if rec := do(tt.h, http.MethodGet, tt.path, "", nil); rec.Code != tt.code {
			t.Errorf("%s: code = %d, want %d", tt.name, rec.Code, tt.code)
		}
	}
}
