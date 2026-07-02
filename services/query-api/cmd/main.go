// Command query-api runs the AgentMesh Query API service: a REST surface
// over the trace store (Architecture.md §2), authenticated via the same
// API-key mechanism as the Collector (Architecture.md §13).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/agentmesh/agentmesh/services/query-api/internal/authz"
	"github.com/agentmesh/agentmesh/services/query-api/internal/graphql"
	"github.com/agentmesh/agentmesh/services/query-api/internal/rest"
	"github.com/agentmesh/agentmesh/services/query-api/internal/store"
	"github.com/agentmesh/agentmesh/shared/authkeys"
	"github.com/agentmesh/agentmesh/shared/config"
	"github.com/agentmesh/agentmesh/shared/logging"
	"github.com/jackc/pgx/v5/pgxpool"
)

type serviceConfig struct {
	HTTPAddr           string `env:"AGENTMESH_QUERYAPI_HTTP_ADDR" yaml:"http_addr"`
	ClickHouseAddr     string `env:"AGENTMESH_CLICKHOUSE_ADDR" yaml:"clickhouse_addr"`
	ClickHouseUser     string `env:"AGENTMESH_CLICKHOUSE_USER" yaml:"clickhouse_user"`
	ClickHousePassword string `env:"AGENTMESH_CLICKHOUSE_PASSWORD" yaml:"clickhouse_password"`
	PostgresDSN        string `env:"AGENTMESH_POSTGRES_DSN" yaml:"postgres_dsn"`
	APIKeyCacheTTLSecs int    `env:"AGENTMESH_APIKEY_CACHE_TTL_SECONDS" yaml:"apikey_cache_ttl_seconds"`
}

func main() {
	logger := logging.New("query-api", slog.LevelInfo)
	if err := run(logger); err != nil {
		logger.Error("query-api exited with error", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg := serviceConfig{
		HTTPAddr:           ":8080",
		ClickHouseAddr:     "localhost:9000",
		ClickHouseUser:     "default",
		ClickHousePassword: "agentmesh",
		PostgresDSN:        "postgres://agentmesh:agentmesh@localhost:15432/agentmesh",
		APIKeyCacheTTLSecs: 60,
	}
	if err := config.NewLoader().Load(&cfg, os.Getenv("AGENTMESH_CONFIG_FILE")); err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	chConn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{cfg.ClickHouseAddr},
		Auth: clickhouse.Auth{Username: cfg.ClickHouseUser, Password: cfg.ClickHousePassword},
	})
	if err != nil {
		return fmt.Errorf("opening clickhouse connection: %w", err)
	}
	defer chConn.Close()
	if err := chConn.Ping(ctx); err != nil {
		return fmt.Errorf("pinging clickhouse: %w", err)
	}
	logger.Info("connected to clickhouse")

	pgPool, err := pgxpool.New(ctx, cfg.PostgresDSN)
	if err != nil {
		return fmt.Errorf("connecting to postgres: %w", err)
	}
	defer pgPool.Close()
	if err := pgPool.Ping(ctx); err != nil {
		return fmt.Errorf("pinging postgres: %w", err)
	}
	logger.Info("connected to postgres")

	authStore := authkeys.NewCachedStore(
		authkeys.NewPostgresStore(pgPool),
		time.Duration(cfg.APIKeyCacheTTLSecs)*time.Second,
	)
	reader := store.NewClickHouseReader(chConn)
	tracesHandler := rest.NewTracesHandler(reader, authz.ProjectIDFromRequest)
	setupHandler := rest.NewSetupHandler(pgPool)
	mcpRegistryHandler := rest.NewMCPRegistryHandler(pgPool, authz.ProjectIDFromRequest)
	graphqlHandler, err := graphql.NewHandler(reader, authz.ProjectIDFromRequest)
	if err != nil {
		return fmt.Errorf("building graphql handler: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/v1/traces", authz.Middleware(authStore)(tracesHandler))
	mux.Handle("/v1/traces/", authz.Middleware(authStore)(tracesHandler))
	mux.Handle("/v1/graphql", authz.Middleware(authStore)(graphqlHandler))
	mux.Handle("/v1/mcp/", authz.Middleware(authStore)(mcpRegistryHandler))
	mux.Handle("/v1/setup", setupHandler)

	handler := rest.CORSMiddleware(mux)

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("query-api listening", slog.String("addr", cfg.HTTPAddr))
		serveErr <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received, stopping gracefully")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-serveErr:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	}
}
