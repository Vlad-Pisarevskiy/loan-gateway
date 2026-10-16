package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"
)

func TestMetricsServerServesRegistry(t *testing.T) {
	reg := telemetry.NewRegistry()
	telemetry.NewMetrics(reg).LeaseLost()
	srv := newMetricsServer(":0", reg)
	if srv.ReadHeaderTimeout <= 0 {
		t.Fatal("ReadHeaderTimeout is not set: a slow client holds a goroutine forever")
	}
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "loangw_lease_lost_total 1") || !strings.Contains(body, "go_goroutines") {
		t.Fatalf("code %d, body:\n%s", rec.Code, body)
	}
}

func TestStartMetricsReportsBusyPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	errs, stop := startMetrics(ln.Addr().String(), telemetry.NewRegistry())
	defer func() { _ = stop(context.Background()) }()
	select {
	case err := <-errs:
		if err == nil {
			t.Fatal("nil error for a busy port")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("busy METRICS_ADDR is not reported")
	}
}

// TestQueueStatsTimeoutFitsScrape: gauge очереди читаются из БД при опросе /metrics; их таймаут должен
// оставлять запас до таймаута опроса Prometheus, иначе зависшая БД выглядит как недоступный воркер.
func TestQueueStatsTimeoutFitsScrape(t *testing.T) {
	cfg, err := os.ReadFile("../../deploy/observability/prometheus.yml")
	if err != nil {
		t.Fatal(err)
	}
	limits := regexp.MustCompile(`(?m)^\s*scrape_(?:interval|timeout):\s*(\d+)s\s*$`).FindAllStringSubmatch(string(cfg), -1)
	if len(limits) == 0 {
		t.Fatal("prometheus.yml sets no scrape_interval in seconds")
	}
	for _, l := range limits {
		n, _ := strconv.Atoi(l[1])
		if limit := time.Duration(n) * time.Second; 2*queueStatsTimeout > limit {
			t.Errorf("queue stats timeout %v leaves no margin within scrape limit %v", queueStatsTimeout, limit)
		}
	}
}
