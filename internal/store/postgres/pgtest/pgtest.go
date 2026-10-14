// Package pgtest поднимает PostgreSQL в Docker для интеграционных тестов.
package pgtest

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/store/postgres"
)

var (
	once     sync.Once
	adminURL string
	startErr error
	seq      atomic.Int64
)

// NewPool создаёт отдельную БД с применёнными миграциями в общем для процесса тестов
// контейнере PostgreSQL и возвращает пул к ней. В режиме -short тест пропускается.
func NewPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	once.Do(startContainer)
	if startErr != nil {
		t.Fatalf("start postgres container: %v", startErr)
	}
	ctx := context.Background()
	dbURL, err := createDatabase(ctx, fmt.Sprintf("test_%d", seq.Add(1)))
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	if err := postgres.Migrate(ctx, dbURL); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// startContainer запускает контейнер один раз на процесс; его удалит Ryuk из testcontainers.
func startContainer() {
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("postgres"),
		tcpostgres.WithUsername("gateway"),
		tcpostgres.WithPassword("gateway"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		startErr = err
		return
	}
	adminURL, startErr = ctr.ConnectionString(ctx, "sslmode=disable")
}

func createDatabase(ctx context.Context, name string) (string, error) {
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return "", err
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		return "", err
	}
	u, err := url.Parse(adminURL)
	if err != nil {
		return "", err
	}
	u.Path = "/" + name
	return u.String(), nil
}
