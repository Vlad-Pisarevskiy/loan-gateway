package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
)

type fakeService struct {
	create func(ctx context.Context, key string, in application.Input) (application.View, error)
	get    func(ctx context.Context, id uuid.UUID) (application.View, error)
}

func (f *fakeService) Create(ctx context.Context, key string, in application.Input) (application.View, error) {
	return f.create(ctx, key, in)
}

func (f *fakeService) Get(ctx context.Context, id uuid.UUID) (application.View, error) {
	return f.get(ctx, id)
}

type fakePinger struct{ err error }

func (p fakePinger) Ping(context.Context) error { return p.err }

func testLogger(t *testing.T) *slog.Logger {
	return slog.New(slog.NewTextHandler(t.Output(), nil))
}

func do(h http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// decodeProblem проверяет Content-Type и разбирает тело problem+json.
func decodeProblem(t *testing.T, rec *httptest.ResponseRecorder) problem {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type = %q, want application/problem+json", ct)
	}
	var p problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if p.Status != rec.Code {
		t.Fatalf("problem status %d != response code %d", p.Status, rec.Code)
	}
	return p
}
