// Command realtime-gateway runs the Realtime Gateway service: a stateless
// WebSocket/Redis bridge pushing live span events to connected Web
// Console/CLI sessions (Architecture.md §2). It never touches ClickHouse
// or Postgres beyond API-key validation — Query API remains the sole
// read path for historical data (System Design.md §5).
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/agentmesh/agentmesh/services/realtime-gateway/internal/hub"
	"github.com/agentmesh/agentmesh/services/realtime-gateway/internal/pubsub"
	"github.com/agentmesh/agentmesh/services/realtime-gateway/internal/ws"
	"github.com/agentmesh/agentmesh/shared/authkeys"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	pgPool, err := pgxpool.New(ctx, envOrDefault("AGENTMESH_POSTGRES_DSN",
		"postgres://agentmesh:agentmesh@localhost:15432/agentmesh?sslmode=disable"))
	if err != nil {
		logger.Error("failed to connect to postgres", "err", err)
		os.Exit(1)
	}
	defer pgPool.Close()

	redisClient := redis.NewClient(&redis.Options{
		Addr: envOrDefault("AGENTMESH_REDIS_ADDR", "localhost:16379"),
	})
	defer redisClient.Close()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		logger.Error("failed to connect to redis", "err", err)
		os.Exit(1)
	}

	authStore := authkeys.NewCachedStore(authkeys.NewPostgresStore(pgPool), 60*time.Second)

	h := hub.New(logger)
	sub := pubsub.New(redisClient, h, logger)
	h.AttachSubscriber(sub)

	handler := ws.NewHandler(h, authStore, logger)

	mux := http.NewServeMux()
	mux.Handle("/v1/tail", handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	addr := envOrDefault("AGENTMESH_REALTIME_HTTP_ADDR", ":8081")
	srv := &http.Server{Addr: addr, Handler: mux}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	logger.Info("realtime-gateway listening", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server failed", "err", err)
		os.Exit(1)
	}
}
