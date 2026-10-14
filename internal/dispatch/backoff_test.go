package dispatch

import (
	"math/rand/v2"
	"testing"
	"time"
)

func TestBackoffUpperBound(t *testing.T) {
	upper := func(n int64) int64 { return n - 1 } // всегда максимум диапазона
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{1, time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{7, time.Minute}, // 64 с упираются в потолок
		{100, time.Minute},
	}
	for _, tt := range tests {
		if got := Backoff(tt.attempt, time.Second, time.Minute, upper); got != tt.want {
			t.Errorf("Backoff(%d) = %s, want %s", tt.attempt, got, tt.want)
		}
	}
}

func TestBackoffLowerBound(t *testing.T) {
	zero := func(int64) int64 { return 0 }
	if got := Backoff(3, time.Second, time.Minute, zero); got != 0 {
		t.Fatalf("got %s, want 0", got)
	}
}

func TestBackoffStaysInRange(t *testing.T) {
	for range 1000 {
		if d := Backoff(3, time.Second, time.Minute, rand.Int64N); d < 0 || d > 4*time.Second {
			t.Fatalf("Backoff(3) = %s, want within [0, 4s]", d)
		}
	}
}
