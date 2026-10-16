package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"
)

// newMetricsServer — служебный HTTP-сервер с GET /metrics на METRICS_ADDR (раздел 14.1 спеки).
func newMetricsServer(addr string, g prometheus.Gatherer) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", telemetry.Handler(g))
	return &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
}

// startMetrics поднимает сервер метрик в фоне. Ошибка запуска (например, занятый порт) приходит
// в канал; stop останавливает сервер в пределах ctx.
func startMetrics(addr string, g prometheus.Gatherer) (<-chan error, func(context.Context) error) {
	srv := newMetricsServer(addr, g)
	errs := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errs <- fmt.Errorf("metrics server: %w", err)
		}
	}()
	return errs, srv.Shutdown
}

// queueStatsTimeout — таймаут чтения gauge очереди из БД при опросе /metrics (раздел 14.2 спеки). Он вдвое
// меньше таймаута опроса Prometheus (5 с): иначе зависшая БД выглядит как недоступный воркер и с дашборда
// пропадают все его метрики.
const queueStatsTimeout = 2 * time.Second
