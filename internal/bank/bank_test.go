package bank

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStatusErrorClasses(t *testing.T) {
	for _, code := range []int{429, 500, 502, 503} {
		err := StatusError(code)
		if !errors.Is(err, ErrTemporary) || IsPermanent(err) {
			t.Errorf("status %d: want temporary error, got %v", code, err)
		}
	}
	for _, code := range []int{400, 404, 409, 422} {
		if err := StatusError(code); !IsPermanent(err) {
			t.Errorf("status %d: want permanent error, got %v", code, err)
		}
	}
}

func TestIsPermanentTreatsUnclassifiedAsTemporary(t *testing.T) {
	if IsPermanent(errors.New("boom")) {
		t.Fatal("unclassified error must be temporary")
	}
	if !IsPermanent(fmt.Errorf("wrapped: %w", ErrPermanent)) {
		t.Fatal("wrapped permanent error must stay permanent")
	}
}

func TestPostJSONSendsBodyAndHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-Key") != "k-1" {
			t.Errorf("unexpected request: %s %v", r.Method, r.Header)
		}
		var got map[string]int
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil || got["a"] != 1 {
			t.Errorf("unexpected body %v (%v)", got, err)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	status, body, err := PostJSON(t.Context(), srv.Client(), srv.URL, map[string]string{"X-Key": "k-1"}, map[string]int{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusCreated || string(body) != `{"ok":true}` {
		t.Fatalf("got %d %q", status, body)
	}
}

func TestPostJSONNetworkErrorIsTemporary(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	_, _, err := PostJSON(t.Context(), http.DefaultClient, url, nil, struct{}{})
	if !errors.Is(err, ErrTemporary) {
		t.Fatalf("want temporary error, got %v", err)
	}
}

func TestPostJSONTimeoutIsTemporary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(time.Second):
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, _, err := PostJSON(ctx, srv.Client(), srv.URL, nil, struct{}{})
	if !errors.Is(err, ErrTemporary) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want temporary deadline error, got %v", err)
	}
}
