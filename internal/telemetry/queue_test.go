package telemetry_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry/telemetrytest"
)

func TestQueueStatsGauges(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := telemetry.NewMetrics(reg)
	m.RegisterQueueStats(reg, func(context.Context) (telemetry.QueueStats, error) {
		return telemetry.QueueStats{
			Counts: []telemetry.QueueCount{
				{Bank: "eastbank", Status: "queued", Count: 3},
				{Bank: "westbank", Status: "waiting_callback", Count: 1},
			},
			OldestAge: 90 * time.Second,
		}, nil
	}, time.Second)

	if got := telemetrytest.Value(t, reg, "loangw_bank_requests", map[string]string{"bank": "eastbank", "status": "queued"}); got != 3 {
		t.Errorf("eastbank queued = %v, want 3", got)
	}
	if got := telemetrytest.Value(t, reg, "loangw_bank_requests", map[string]string{"bank": "westbank", "status": "waiting_callback"}); got != 1 {
		t.Errorf("westbank waiting_callback = %v, want 1", got)
	}
	if got := telemetrytest.Value(t, reg, "loangw_queue_oldest_age_seconds", nil); got != 90 {
		t.Errorf("oldest age = %v, want 90", got)
	}
}

func TestQueueStatsErrorDropsGauges(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := telemetry.NewMetrics(reg)
	m.RegisterQueueStats(reg, func(context.Context) (telemetry.QueueStats, error) {
		return telemetry.QueueStats{}, errors.New("connection refused")
	}, time.Second)

	if telemetrytest.Has(t, reg, "loangw_bank_requests") || telemetrytest.Has(t, reg, "loangw_queue_oldest_age_seconds") {
		t.Fatal("queue gauges are exported although stats failed")
	}
	// Каждый сбор метрик повторяет запрос, поэтому счётчик не меньше 1.
	if got := telemetrytest.Value(t, reg, "loangw_queue_errors_total", map[string]string{"op": "stats"}); got < 1 {
		t.Fatalf("queue_errors{op=stats} = %v, want >= 1", got)
	}
}

func TestQueueStatsDoesNotHangScrape(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := telemetry.NewMetrics(reg)
	m.RegisterQueueStats(reg, func(ctx context.Context) (telemetry.QueueStats, error) {
		<-ctx.Done() // как запрос к зависшей БД
		return telemetry.QueueStats{}, ctx.Err()
	}, 50*time.Millisecond)

	start := time.Now()
	if _, err := reg.Gather(); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("scrape took %s: stats query must respect its timeout", d)
	}
	if telemetrytest.Has(t, reg, "loangw_queue_oldest_age_seconds") {
		t.Fatal("oldest age is exported although stats timed out")
	}
}
