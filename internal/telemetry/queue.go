package telemetry

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// QueueCount — число незавершённых запросов в банк Bank в статусе Status.
type QueueCount struct {
	Bank   string
	Status string
	Count  int64
}

// QueueStats — снимок очереди для gauge-метрик (раздел 14.2 спеки).
type QueueStats struct {
	Counts    []QueueCount  // по банкам и статусам queued, sending, waiting_callback
	OldestAge time.Duration // возраст самой старой готовой задачи; 0 — таких нет
}

// QueueStatsFunc читает состояние очереди; реализация — store/postgres.Queue.Stats.
type QueueStatsFunc func(ctx context.Context) (QueueStats, error)

// RegisterQueueStats регистрирует gauge очереди: при каждом сборе метрик вызывается stats
// с таймаутом timeout. При ошибке gauge не отдаются, растёт loangw_queue_errors_total{op="stats"}.
// m не должен быть nil.
func (m *Metrics) RegisterQueueStats(reg prometheus.Registerer, stats QueueStatsFunc, timeout time.Duration) {
	reg.MustRegister(&queueCollector{
		stats:   stats,
		timeout: timeout,
		failed:  func() { m.QueueError(OpStats) },
		requests: prometheus.NewDesc("loangw_bank_requests",
			"Unfinished bank requests by bank and status.", []string{"bank", "status"}, nil),
		oldest: prometheus.NewDesc("loangw_queue_oldest_age_seconds",
			"Age of the oldest queued request that is ready to send.", nil, nil),
	})
}

// queueCollector читает gauge очереди из БД при каждом сборе метрик.
type queueCollector struct {
	stats    QueueStatsFunc
	timeout  time.Duration
	failed   func()
	requests *prometheus.Desc
	oldest   *prometheus.Desc
}

func (c *queueCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.requests
	ch <- c.oldest
}

func (c *queueCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	s, err := c.stats(ctx)
	if err != nil {
		c.failed()
		return
	}
	for _, q := range s.Counts {
		ch <- prometheus.MustNewConstMetric(c.requests, prometheus.GaugeValue, float64(q.Count), q.Bank, q.Status)
	}
	ch <- prometheus.MustNewConstMetric(c.oldest, prometheus.GaugeValue, s.OldestAge.Seconds())
}
