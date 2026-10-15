// Package config читает конфигурацию шлюза и заглушки банков из переменных окружения.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// Bank — настройки одного включённого банка.
type Bank struct {
	Code          string
	URL           string
	Timeout       time.Duration
	WebhookSecret string
}

// Gateway — конфигурация бинарника gateway (раздел 9 спеки).
type Gateway struct {
	DatabaseURL     string
	HTTPAddr        string
	MetricsAddr     string
	LogLevel        slog.Level
	ShutdownTimeout time.Duration
	PollInterval    time.Duration
	BatchSize       int
	Concurrency     int
	Lease           time.Duration
	MaxAttempts     int
	BaseDelay       time.Duration
	MaxDelay        time.Duration
	CallbackTimeout time.Duration
	SweepInterval   time.Duration
	WebhookMaxSkew  time.Duration
	Banks           []Bank // только банки с заданным BANK_<CODE>_URL, в порядке knownBanks
}

// LoadGateway читает конфигурацию gateway. knownBanks — коды банков, для которых есть адаптеры;
// банк включён, если задан BANK_<CODE>_URL. Отсутствие банков не ошибка: migrate они не нужны.
func LoadGateway(getenv func(string) string, knownBanks []string) (Gateway, error) {
	r := reader{getenv: getenv}
	cfg := Gateway{
		DatabaseURL:     r.required("DATABASE_URL"),
		HTTPAddr:        r.str("HTTP_ADDR", ":8080"),
		MetricsAddr:     r.str("METRICS_ADDR", ":9100"),
		LogLevel:        r.logLevel("LOG_LEVEL", slog.LevelInfo),
		ShutdownTimeout: r.positiveDuration("SHUTDOWN_TIMEOUT", 20*time.Second),
		PollInterval:    r.positiveDuration("WORKER_POLL_INTERVAL", 500*time.Millisecond),
		BatchSize:       r.positiveInt("WORKER_BATCH_SIZE", 10),
		Concurrency:     r.positiveInt("WORKER_CONCURRENCY", 10),
		Lease:           r.positiveDuration("WORKER_LEASE", 30*time.Second),
		MaxAttempts:     r.positiveInt("RETRY_MAX_ATTEMPTS", 5),
		BaseDelay:       r.positiveDuration("RETRY_BASE_DELAY", time.Second),
		MaxDelay:        r.positiveDuration("RETRY_MAX_DELAY", time.Minute),
		CallbackTimeout: r.positiveDuration("CALLBACK_TIMEOUT", 10*time.Minute),
		SweepInterval:   r.positiveDuration("SWEEP_INTERVAL", 5*time.Second),
		WebhookMaxSkew:  r.positiveDuration("WEBHOOK_MAX_SKEW", 5*time.Minute),
	}
	for _, code := range knownBanks {
		prefix := "BANK_" + strings.ToUpper(code) + "_"
		baseURL := getenv(prefix + "URL")
		if baseURL == "" {
			continue
		}
		b := Bank{
			Code:          code,
			URL:           baseURL,
			Timeout:       r.positiveDuration(prefix+"TIMEOUT", 5*time.Second),
			WebhookSecret: getenv(prefix + "WEBHOOK_SECRET"),
		}
		if cfg.Lease <= b.Timeout {
			r.fail(fmt.Errorf("WORKER_LEASE (%s) must be greater than %sTIMEOUT (%s)", cfg.Lease, prefix, b.Timeout))
		}
		cfg.Banks = append(cfg.Banks, b)
	}
	return cfg, r.err()
}

// Mockbank — конфигурация заглушки банка (раздел 6.5 спеки).
type Mockbank struct {
	Addr           string
	Dialect        string // north | east | west
	Latency        time.Duration
	FailFirst      int
	CallbackURL    string
	CallbackSecret string
	CallbackDelay  time.Duration
	CallbackSkip   bool
}

// LoadMockbank читает конфигурацию заглушки.
func LoadMockbank(getenv func(string) string) (Mockbank, error) {
	r := reader{getenv: getenv}
	cfg := Mockbank{
		Addr:           r.str("MOCKBANK_ADDR", ":8090"),
		Dialect:        r.required("MOCKBANK_DIALECT"),
		Latency:        r.nonNegativeDuration("MOCKBANK_LATENCY", 0),
		FailFirst:      r.nonNegativeInt("MOCKBANK_FAIL_FIRST", 0),
		CallbackURL:    getenv("MOCKBANK_CALLBACK_URL"),
		CallbackSecret: getenv("MOCKBANK_CALLBACK_SECRET"),
		CallbackDelay:  r.nonNegativeDuration("MOCKBANK_CALLBACK_DELAY", 5*time.Second),
		CallbackSkip:   r.boolean("MOCKBANK_CALLBACK_SKIP", false),
	}
	switch cfg.Dialect {
	case "", "north", "east": // пустой диалект уже отмечен в required
	case "west":
		if !cfg.CallbackSkip && cfg.CallbackURL == "" {
			r.fail(errors.New("MOCKBANK_CALLBACK_URL is required for the west dialect"))
		}
		if !cfg.CallbackSkip && cfg.CallbackSecret == "" {
			r.fail(errors.New("MOCKBANK_CALLBACK_SECRET is required for the west dialect"))
		}
	default:
		r.fail(fmt.Errorf("MOCKBANK_DIALECT must be north, east or west, got %q", cfg.Dialect))
	}
	return cfg, r.err()
}

// reader читает переменные и копит ошибки, чтобы показать их все разом.
type reader struct {
	getenv func(string) string
	errs   []error
}

func (r *reader) fail(err error) {
	r.errs = append(r.errs, err)
}

func (r *reader) err() error {
	return errors.Join(r.errs...)
}

func (r *reader) str(name, def string) string {
	if v := r.getenv(name); v != "" {
		return v
	}
	return def
}

func (r *reader) required(name string) string {
	v := r.getenv(name)
	if v == "" {
		r.fail(fmt.Errorf("%s is required", name))
	}
	return v
}

func (r *reader) positiveDuration(name string, def time.Duration) time.Duration {
	return r.duration(name, def, false)
}

func (r *reader) nonNegativeDuration(name string, def time.Duration) time.Duration {
	return r.duration(name, def, true)
}

func (r *reader) duration(name string, def time.Duration, allowZero bool) time.Duration {
	v := r.getenv(name)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 || (d == 0 && !allowZero) {
		r.fail(fmt.Errorf("%s: invalid duration %q", name, v))
		return def
	}
	return d
}

func (r *reader) positiveInt(name string, def int) int {
	return r.integer(name, def, 1)
}

func (r *reader) nonNegativeInt(name string, def int) int {
	return r.integer(name, def, 0)
}

func (r *reader) integer(name string, def, minValue int) int {
	v := r.getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < minValue {
		r.fail(fmt.Errorf("%s: must be an integer >= %d, got %q", name, minValue, v))
		return def
	}
	return n
}

func (r *reader) boolean(name string, def bool) bool {
	v := r.getenv(name)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		r.fail(fmt.Errorf("%s: must be true or false, got %q", name, v))
		return def
	}
	return b
}

func (r *reader) logLevel(name string, def slog.Level) slog.Level {
	v := r.getenv(name)
	if v == "" {
		return def
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(v)); err != nil {
		r.fail(fmt.Errorf("%s: invalid level %q", name, v))
		return def
	}
	return level
}
