package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/agentmesh/agentmesh/services/anomaly-detector/internal/detector"
	"github.com/agentmesh/agentmesh/shared/config"
	"github.com/agentmesh/agentmesh/shared/logging"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Config struct {
	DBURL    string `env:"DATABASE_URL"`
	RedisURL string `env:"REDIS_URL"`
	LogLevel string `env:"LOG_LEVEL"`
}

func main() {
	var cfg Config
	cfg.DBURL = "postgres://postgres:postgres@localhost:5432/agentmesh?sslmode=disable"
	cfg.RedisURL = "redis://localhost:6379/0"
	cfg.LogLevel = "info"

	if err := config.NewLoader().Load(&cfg, "config.yaml"); err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		level = slog.LevelInfo
	}
	logger := logging.New("anomaly-detector", level)
	slog.SetDefault(logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbpool, err := pgxpool.New(ctx, cfg.DBURL)
	if err != nil {
		logger.Error("failed to connect to db", "err", err)
		os.Exit(1)
	}
	defer dbpool.Close()

	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		logger.Error("failed to parse redis url", "err", err)
		os.Exit(1)
	}
	rdb := redis.NewClient(opts)
	defer rdb.Close()

	if err := rdb.Ping(ctx).Err(); err != nil {
		logger.Error("failed to connect to redis", "err", err)
		os.Exit(1)
	}

	det := detector.New(dbpool, rdb, logger)

	errCh := make(chan error, 1)
	go func() {
		errCh <- det.Start(ctx)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-sigCh:
		logger.Info("shutting down")
		cancel()
		<-errCh
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			logger.Error("detector stopped with error", "err", err)
			os.Exit(1)
		}
	}
}
