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
// с таймаутом timeout. Для каждого банка из banks gauge отдаётся по каждому статусу, с нулём там, где
// в Stats строки нет: иначе пустая очередь не даёт ни одной серии. При ошибке gauge не отдаются,
// растёт loangw_queue_errors_total{op="stats"}. m не должен быть nil.
func (m *Metrics) RegisterQueueStats(reg prometheus.Registerer, stats QueueStatsFunc, timeout time.Duration, banks []string) {
	reg.MustRegister(&queueCollector{
		stats:   stats,
		timeout: timeout,
		banks:   banks,
		failed:  func() { m.QueueError(OpStats) },
		requests: prometheus.NewDesc("loangw_bank_requests",
			"Unfinished bank requests by bank and status.", []string{"bank", "status"}, nil),
		oldest: prometheus.NewDesc("loangw_queue_oldest_age_seconds",
			"Age of the oldest queued request that is ready to send.", nil, nil),
	})
}

// queueStatuses — статусы незавершённых запросов из раздела 14.2 спеки.
var queueStatuses = []string{"queued", "sending", "waiting_callback"}

// queueCollector читает gauge очереди из БД при каждом сборе метрик.
type queueCollector struct {
	stats    QueueStatsFunc
	timeout  time.Duration
	banks    []string
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
	seen := make(map[[2]string]bool, len(s.Counts))
	for _, q := range s.Counts {
		seen[[2]string{q.Bank, q.Status}] = true
		ch <- prometheus.MustNewConstMetric(c.requests, prometheus.GaugeValue, float64(q.Count), q.Bank, q.Status)
	}
	// Пустая очередь не даёт строк в Stats; нулевые серии включённых банков заполняем здесь.
	for _, b := range c.banks {
		for _, st := range queueStatuses {
			if !seen[[2]string{b, st}] {
				ch <- prometheus.MustNewConstMetric(c.requests, prometheus.GaugeValue, 0, b, st)
			}
		}
	}
	ch <- prometheus.MustNewConstMetric(c.oldest, prometheus.GaugeValue, s.OldestAge.Seconds())
}
