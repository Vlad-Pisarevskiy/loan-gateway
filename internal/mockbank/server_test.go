package mockbank

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// echoHandler — минимальный диалект для проверки общей механики server.
func echoHandler(s *server, status int, calls *int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.handle(w, r, r.Header.Get("Key"), func(body []byte) (int, any) {
			*calls++ // produce вызывается под мьютексом server
			return status, map[string]string{"echo": string(body)}
		})
	})
}

func send(h http.Handler, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	if key != "" {
		req.Header.Set("Key", key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestServerFailFirstPerKey(t *testing.T) {
	calls := 0
	h := echoHandler(newServer(Options{FailFirst: 2}), http.StatusOK, &calls)
	for i, want := range []int{503, 503, 200, 200} {
		if rec := send(h, "k-1", "a"); rec.Code != want {
			t.Fatalf("request %d: code %d, want %d", i+1, rec.Code, want)
		}
	}
	if rec := send(h, "k-2", "a"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("new key: code %d, want 503 — failures are counted per key", rec.Code)
	}
	if calls != 1 {
		t.Fatalf("produce called %d times, want 1", calls)
	}
}

func TestServerReplaysSuccessfulResponse(t *testing.T) {
	calls := 0
	h := echoHandler(newServer(Options{}), http.StatusOK, &calls)
	first := send(h, "k-1", "a")
	second := send(h, "k-1", "b")
	if second.Code != http.StatusOK || second.Body.String() != first.Body.String() {
		t.Fatalf("repeat returned %d %s, want the saved %s", second.Code, second.Body, first.Body)
	}
	if calls != 1 {
		t.Fatalf("produce called %d times, want 1", calls)
	}
}

func TestServerDoesNotSaveErrors(t *testing.T) {
	calls := 0
	h := echoHandler(newServer(Options{}), http.StatusBadRequest, &calls)
	send(h, "k-1", "a")
	send(h, "k-1", "a")
	if calls != 2 {
		t.Fatalf("produce called %d times, want 2 — errors must not be cached", calls)
	}
}

func TestServerRequiresKey(t *testing.T) {
	calls := 0
	if rec := send(echoHandler(newServer(Options{}), http.StatusOK, &calls), "", "a"); rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
}

func TestServerLatency(t *testing.T) {
	calls := 0
	h := echoHandler(newServer(Options{Latency: 50 * time.Millisecond}), http.StatusOK, &calls)
	start := time.Now()
	send(h, "k-1", "a")
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Fatalf("response took %s, want at least 50ms", elapsed)
	}
}
