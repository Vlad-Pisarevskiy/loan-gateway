package dispatch

import "time"

// Backoff — пауза перед следующей попыткой после неудачной попытки attempt (с 1), full jitter:
// случайная в [0, min(maxDelay, base·2^(attempt−1))]. rnd(n) возвращает число из [0, n).
func Backoff(attempt int, base, maxDelay time.Duration, rnd func(int64) int64) time.Duration {
	d := base
	for i := 1; i < attempt && d < maxDelay; i++ {
		d *= 2
	}
	d = min(d, maxDelay)
	return time.Duration(rnd(int64(d) + 1))
}
