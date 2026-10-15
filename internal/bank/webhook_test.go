package bank

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"
)

var testSecret = []byte("test-secret")

func TestSignWebhookFormat(t *testing.T) {
	mac := hmac.New(sha256.New, testSecret)
	mac.Write([]byte("1760000000.{}"))
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if got := SignWebhook(testSecret, "1760000000", []byte("{}")); got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestVerifyWebhook(t *testing.T) {
	now := time.Unix(1_760_000_000, 0)
	ts := "1760000000"
	body := []byte(`{"application_id": "w-1",  "result":"approved"}`) // пробелы сохраняются: подписывается сырое тело
	sig := SignWebhook(testSecret, ts, body)

	tests := []struct {
		name    string
		secret  []byte
		ts, sig string
		body    []byte
		now     time.Time
		wantErr bool
	}{
		{"valid", testSecret, ts, sig, body, now, false},
		{"valid within skew", testSecret, ts, sig, body, now.Add(4 * time.Minute), false},
		{"wrong secret", []byte("other"), ts, sig, body, now, true},
		{"re-encoded body", testSecret, ts, sig, []byte(`{"application_id":"w-1","result":"approved"}`), now, true},
		{"stale timestamp", testSecret, ts, sig, body, now.Add(6 * time.Minute), true},
		{"timestamp from future", testSecret, ts, sig, body, now.Add(-6 * time.Minute), true},
		{"missing signature", testSecret, ts, "", body, now, true},
		{"missing timestamp", testSecret, "", sig, body, now, true},
		{"bad timestamp", testSecret, "abc", sig, body, now, true},
		{"millisecond timestamp", testSecret, "1760000000000", SignWebhook(testSecret, "1760000000000", body), body, now, true},
		{"far future timestamp", testSecret, "99999999999", SignWebhook(testSecret, "99999999999", body), body, now, true},
		{"max int64 timestamp", testSecret, "9223372036854775807", SignWebhook(testSecret, "9223372036854775807", body), body, now, true},
		{"exactly at skew boundary", testSecret, ts, sig, body, now.Add(5 * time.Minute), false},
		{"timestamp is part of signature", testSecret, "1760000001", sig, body, now, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyWebhook(tt.secret, tt.ts, tt.sig, tt.body, tt.now, 5*time.Minute)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrBadSignature) {
				t.Fatalf("err = %v, want ErrBadSignature", err)
			}
		})
	}
}
