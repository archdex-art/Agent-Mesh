package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/agentmesh/agentmesh/services/alerting-service/internal/worker"
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

	w := worker.New(pgPool, logger)
	if err := w.Run(ctx); err != nil && err != context.Canceled {
		logger.Error("Worker failed", "err", err)
		os.Exit(1)
	}

	logger.Info("Worker shut down cleanly")
}
