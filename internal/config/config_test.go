package config

import (
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"
)

func env(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

var knownBanks = []string{"eastbank", "northbank", "westbank"}

func TestLoadGatewayDefaults(t *testing.T) {
	cfg, err := LoadGateway(env(map[string]string{
		"DATABASE_URL":       "postgres://db",
		"BANK_NORTHBANK_URL": "http://northbank:8090",
	}), knownBanks)
	if err != nil {
		t.Fatal(err)
	}
	want := Gateway{
		DatabaseURL:     "postgres://db",
		HTTPAddr:        ":8080",
		MetricsAddr:     ":9100",
		LogLevel:        slog.LevelInfo,
		ShutdownTimeout: 20 * time.Second,
		PollInterval:    500 * time.Millisecond,
		BatchSize:       10,
		Concurrency:     10,
		Lease:           30 * time.Second,
		MaxAttempts:     5,
		BaseDelay:       time.Second,
		MaxDelay:        time.Minute,
		CallbackTimeout: 10 * time.Minute,
		SweepInterval:   5 * time.Second,
		WebhookMaxSkew:  5 * time.Minute,
		Banks:           []Bank{{Code: "northbank", URL: "http://northbank:8090", Timeout: 5 * time.Second}},
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("got  %+v\nwant %+v", cfg, want)
	}
}

func TestLoadGatewayOverrides(t *testing.T) {
	cfg, err := LoadGateway(env(map[string]string{
		"DATABASE_URL":                 "postgres://db",
		"METRICS_ADDR":                 ":9200",
		"HTTP_ADDR":                    ":9090",
		"LOG_LEVEL":                    "debug",
		"WORKER_CONCURRENCY":           "3",
		"RETRY_MAX_ATTEMPTS":           "7",
		"CALLBACK_TIMEOUT":             "30s",
		"BANK_WESTBANK_URL":            "http://westbank:8090",
		"BANK_WESTBANK_TIMEOUT":        "2s",
		"BANK_WESTBANK_WEBHOOK_SECRET": "secret",
	}), knownBanks)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":9090" || cfg.MetricsAddr != ":9200" || cfg.LogLevel != slog.LevelDebug || cfg.Concurrency != 3 ||
		cfg.MaxAttempts != 7 || cfg.CallbackTimeout != 30*time.Second {
		t.Fatalf("overrides not applied: %+v", cfg)
	}
	wantBanks := []Bank{{Code: "westbank", URL: "http://westbank:8090", Timeout: 2 * time.Second, WebhookSecret: "secret"}}
	if !reflect.DeepEqual(cfg.Banks, wantBanks) {
		t.Fatalf("banks = %+v, want %+v", cfg.Banks, wantBanks)
	}
}

func TestLoadGatewayWithoutBanksIsAllowedForMigrate(t *testing.T) {
	cfg, err := LoadGateway(env(map[string]string{"DATABASE_URL": "postgres://db"}), knownBanks)
	if err != nil || len(cfg.Banks) != 0 {
		t.Fatalf("got %+v, %v", cfg.Banks, err)
	}
}

func TestLoadGatewayErrors(t *testing.T) {
	tests := []struct {
		name    string
		vars    map[string]string
		wantErr string
	}{
		{"missing database url", map[string]string{}, "DATABASE_URL is required"},
		{"bad duration", map[string]string{"DATABASE_URL": "x", "WORKER_LEASE": "soon"}, "WORKER_LEASE"},
		{"zero batch size", map[string]string{"DATABASE_URL": "x", "WORKER_BATCH_SIZE": "0"}, "WORKER_BATCH_SIZE"},
		{"bad log level", map[string]string{"DATABASE_URL": "x", "LOG_LEVEL": "loud"}, "LOG_LEVEL"},
		{"lease not above bank timeout", map[string]string{
			"DATABASE_URL": "x", "WORKER_LEASE": "5s", "BANK_NORTHBANK_URL": "http://northbank",
		}, "WORKER_LEASE (5s) must be greater than BANK_NORTHBANK_TIMEOUT (5s)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadGateway(env(tt.vars), knownBanks)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadMockbankDefaults(t *testing.T) {
	cfg, err := LoadMockbank(env(map[string]string{"MOCKBANK_DIALECT": "north"}))
	if err != nil {
		t.Fatal(err)
	}
	want := Mockbank{Addr: ":8090", Dialect: "north", CallbackDelay: 5 * time.Second}
	if cfg != want {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
}

func TestLoadMockbankWest(t *testing.T) {
	cfg, err := LoadMockbank(env(map[string]string{
		"MOCKBANK_DIALECT":         "west",
		"MOCKBANK_LATENCY":         "1s",
		"MOCKBANK_FAIL_FIRST":      "2",
		"MOCKBANK_CALLBACK_URL":    "http://api:8080/v1/webhooks/banks/westbank",
		"MOCKBANK_CALLBACK_SECRET": "secret",
		"MOCKBANK_CALLBACK_DELAY":  "1s",
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := Mockbank{
		Addr:           ":8090",
		Dialect:        "west",
		Latency:        time.Second,
		FailFirst:      2,
		CallbackURL:    "http://api:8080/v1/webhooks/banks/westbank",
		CallbackSecret: "secret",
		CallbackDelay:  time.Second,
	}
	if cfg != want {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
}

func TestLoadMockbankWestWithSkipNeedsNoCallback(t *testing.T) {
	if _, err := LoadMockbank(env(map[string]string{"MOCKBANK_DIALECT": "west", "MOCKBANK_CALLBACK_SKIP": "true"})); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMockbankErrors(t *testing.T) {
	tests := []struct {
		name    string
		vars    map[string]string
		wantErr string
	}{
		{"missing dialect", map[string]string{}, "MOCKBANK_DIALECT is required"},
		{"unknown dialect", map[string]string{"MOCKBANK_DIALECT": "south"}, "MOCKBANK_DIALECT must be"},
		{"west without callback url", map[string]string{"MOCKBANK_DIALECT": "west", "MOCKBANK_CALLBACK_SECRET": "s"}, "MOCKBANK_CALLBACK_URL"},
		{"west without secret", map[string]string{"MOCKBANK_DIALECT": "west", "MOCKBANK_CALLBACK_URL": "http://api"}, "MOCKBANK_CALLBACK_SECRET"},
		{"negative fail first", map[string]string{"MOCKBANK_DIALECT": "north", "MOCKBANK_FAIL_FIRST": "-1"}, "MOCKBANK_FAIL_FIRST"},
		{"bad skip flag", map[string]string{"MOCKBANK_DIALECT": "west", "MOCKBANK_CALLBACK_SKIP": "maybe"}, "MOCKBANK_CALLBACK_SKIP"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadMockbank(env(tt.vars))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}
