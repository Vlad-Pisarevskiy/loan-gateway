package mockbank

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
)

const westSample = `{"fio":"Иванов Иван","dob":"1990-05-17","tel":"+79991234567","salary":"120000.00","job":"employed","amount":"300000.00","period_months":24}`

var westSecret = []byte("west-secret")

// receiver — приёмник вебхуков: проверяет подпись и отвечает кодами из statuses по очереди,
// после их исчерпания — 200.
type receiver struct {
	mu       sync.Mutex
	statuses []int
	bodies   []string
}

func (rc *receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	err := bank.VerifyWebhook(westSecret, r.Header.Get(bank.HeaderWebhookTimestamp),
		r.Header.Get(bank.HeaderWebhookSignature), body, testNow, time.Minute)
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	rc.bodies = append(rc.bodies, string(body))
	status := http.StatusOK
	if len(rc.statuses) > 0 {
		status, rc.statuses = rc.statuses[0], rc.statuses[1:]
	}
	w.WriteHeader(status)
}

func (rc *receiver) received() []string {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return slices.Clone(rc.bodies)
}

func newTestWest(t *testing.T, callbackURL string, skip bool) http.Handler {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return NewWest(ctx, Options{Now: func() time.Time { return testNow }}, CallbackOptions{
		URL:           callbackURL,
		Secret:        westSecret,
		Delay:         10 * time.Millisecond,
		Skip:          skip,
		Retries:       3,
		RetryInterval: 10 * time.Millisecond,
	})
}

func applyWest(t *testing.T, h http.Handler, key, body string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/loans/apply", strings.NewReader(body))
	req.Header.Set("X-Idempotency-Key", key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("apply: code %d, body %s", rec.Code, rec.Body)
	}
	var acc westAccepted
	if err := json.Unmarshal(rec.Body.Bytes(), &acc); err != nil || !strings.HasPrefix(acc.ApplicationID, "w-") {
		t.Fatalf("apply: body %s, err %v", rec.Body, err)
	}
	return acc.ApplicationID
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestWestAcceptsAndSendsSignedCallback(t *testing.T) {
	rc := &receiver{}
	srv := httptest.NewServer(rc)
	defer srv.Close()

	id := applyWest(t, newTestWest(t, srv.URL, false), "k-1", westSample)
	waitFor(t, "callback", func() bool { return len(rc.received()) == 1 })
	want := `{"application_id":"` + id + `","result":"approved","offer":{"amount":"300000.00","rate":"18.90","period_months":24,"monthly":"15108.01"}}`
	if got := rc.received()[0]; got != want {
		t.Fatalf("callback:\n%s\nwant:\n%s", got, want)
	}
}

func TestWestDeclineCallback(t *testing.T) {
	rc := &receiver{}
	srv := httptest.NewServer(rc)
	defer srv.Close()

	body := strings.Replace(westSample, `"salary":"120000.00"`, `"salary":"20000.00"`, 1)
	id := applyWest(t, newTestWest(t, srv.URL, false), "k-1", body)
	waitFor(t, "callback", func() bool { return len(rc.received()) == 1 })
	want := `{"application_id":"` + id + `","result":"declined","reason":"insufficient_income"}`
	if got := rc.received()[0]; got != want {
		t.Fatalf("callback:\n%s\nwant:\n%s", got, want)
	}
}

func TestWestRetriesCallbackUntilAccepted(t *testing.T) {
	rc := &receiver{statuses: []int{http.StatusNotFound, http.StatusNotFound}}
	srv := httptest.NewServer(rc)
	defer srv.Close()

	applyWest(t, newTestWest(t, srv.URL, false), "k-1", westSample)
	waitFor(t, "3 deliveries", func() bool { return len(rc.received()) == 3 })
	time.Sleep(50 * time.Millisecond)
	if n := len(rc.received()); n != 3 {
		t.Fatalf("deliveries = %d, want 3 — retries must stop after success", n)
	}
}

func TestWestRepeatSchedulesOneCallback(t *testing.T) {
	rc := &receiver{}
	srv := httptest.NewServer(rc)
	defer srv.Close()

	h := newTestWest(t, srv.URL, false)
	first := applyWest(t, h, "k-1", westSample)
	second := applyWest(t, h, "k-1", westSample)
	if first != second {
		t.Fatalf("repeat returned %s, want %s", second, first)
	}
	waitFor(t, "callback", func() bool { return len(rc.received()) == 1 })
	time.Sleep(50 * time.Millisecond)
	if n := len(rc.received()); n != 1 {
		t.Fatalf("deliveries = %d, want 1", n)
	}
}

func TestWestSkipsCallback(t *testing.T) {
	rc := &receiver{}
	srv := httptest.NewServer(rc)
	defer srv.Close()

	applyWest(t, newTestWest(t, srv.URL, true), "k-1", westSample)
	time.Sleep(100 * time.Millisecond)
	if n := len(rc.received()); n != 0 {
		t.Fatalf("deliveries = %d, want 0", n)
	}
}
