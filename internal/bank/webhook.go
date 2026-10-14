package bank

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Заголовки вебхука банка (раздел 5.3 спеки).
const (
	HeaderWebhookTimestamp = "X-Webhook-Timestamp"
	HeaderWebhookSignature = "X-Webhook-Signature"
)

// ErrBadSignature — подпись вебхука отсутствует, неверна или устарела.
var ErrBadSignature = errors.New("bad webhook signature")

// SignWebhook возвращает значение заголовка X-Webhook-Signature:
// "sha256=" + hex(HMAC-SHA256(secret, timestamp + "." + body)).
func SignWebhook(secret []byte, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// VerifyWebhook проверяет подпись сырого тела и то, что timestamp (unix-секунды)
// отличается от now не больше чем на maxSkew. Подписи сравниваются за постоянное время.
func VerifyWebhook(secret []byte, timestamp, signature string, body []byte, now time.Time, maxSkew time.Duration) error {
	if timestamp == "" || signature == "" {
		return fmt.Errorf("%w: missing headers", ErrBadSignature)
	}
	sec, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: bad timestamp", ErrBadSignature)
	}
	skew := now.Sub(time.Unix(sec, 0))
	if skew < 0 {
		skew = -skew
	}
	if skew > maxSkew {
		return fmt.Errorf("%w: timestamp outside allowed window", ErrBadSignature)
	}
	want := SignWebhook(secret, timestamp, body)
	if !hmac.Equal([]byte(want), []byte(signature)) {
		return fmt.Errorf("%w: signature mismatch", ErrBadSignature)
	}
	return nil
}
