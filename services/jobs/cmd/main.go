package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/agentmesh/agentmesh/services/jobs/internal/worker"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	pgURL := os.Getenv("AGENTMESH_POSTGRES_URL")
	if pgURL == "" {
		pgURL = "postgres://postgres:postgres@localhost:15432/agentmesh?sslmode=disable"
	}
	pgPool, err := pgxpool.New(ctx, pgURL)
	if err != nil {
		logger.Error("Failed to connect to Postgres", "err", err)
		os.Exit(1)
	}
	defer pgPool.Close()

	chConn, err := connectClickHouse(ctx)
	if err != nil {
		logger.Error("Failed to connect to ClickHouse", "err", err)
		os.Exit(1)
	}
	defer chConn.Close()

	w := worker.New(pgPool, chConn, logger)
	if err := w.Run(ctx); err != nil && err != context.Canceled {
		logger.Error("Worker failed", "err", err)
		os.Exit(1)
	}

	logger.Info("Worker shut down cleanly")
}

func connectClickHouse(ctx context.Context) (driver.Conn, error) {
	addr := os.Getenv("AGENTMESH_CLICKHOUSE_ADDR")
	if addr == "" {
		addr = "localhost:9000"
	}
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{addr},
		Auth: clickhouse.Auth{
			Database: "default",
			Username: "default",
			Password: "",
		},
	})
	if err != nil {
		return nil, err
	}
	if err := conn.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}
	return conn, nil
}
