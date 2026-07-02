// Command replay-engine runs the AgentMesh Replay Engine service:
// trajectory-mode trace reconstruction and execution-mode interactive
// replay (Architecture.md §7, System Design.md §4).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/agentmesh/agentmesh/services/replay-engine/internal/authz"
	"github.com/agentmesh/agentmesh/services/replay-engine/internal/execution"
	"github.com/agentmesh/agentmesh/services/replay-engine/internal/rest"
	"github.com/agentmesh/agentmesh/services/replay-engine/internal/store"
	"github.com/agentmesh/agentmesh/services/replay-engine/internal/trajectory"
	"github.com/agentmesh/agentmesh/shared/authkeys"
	"github.com/agentmesh/agentmesh/shared/config"
	"github.com/agentmesh/agentmesh/shared/logging"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// serviceConfig follows the same env-over-yaml-over-default precedence as
// every other AgentMesh service (Architecture.md §12), with defaults
// matching deploy/docker-compose.yml so `go run ./cmd` against a locally
// running compose stack works with zero configuration.
type serviceConfig struct {
	HTTPAddr           string `env:"AGENTMESH_REPLAYENGINE_HTTP_ADDR" yaml:"http_addr"`
	ClickHouseAddr     string `env:"AGENTMESH_CLICKHOUSE_ADDR" yaml:"clickhouse_addr"`
	ClickHouseUser     string `env:"AGENTMESH_CLICKHOUSE_USER" yaml:"clickhouse_user"`
	ClickHousePassword string `env:"AGENTMESH_CLICKHOUSE_PASSWORD" yaml:"clickhouse_password"`
	PostgresDSN        string `env:"AGENTMESH_POSTGRES_DSN" yaml:"postgres_dsn"`
	APIKeyCacheTTLSecs int    `env:"AGENTMESH_APIKEY_CACHE_TTL_SECONDS" yaml:"apikey_cache_ttl_seconds"`
	MinIOAddr          string `env:"AGENTMESH_MINIO_ADDR" yaml:"minio_addr"`
	MinIOAccessKey     string `env:"AGENTMESH_MINIO_ACCESS_KEY" yaml:"minio_access_key"`
	MinIOSecretKey     string `env:"AGENTMESH_MINIO_SECRET_KEY" yaml:"minio_secret_key"`
	MinIOBucket        string `env:"AGENTMESH_MINIO_BUCKET" yaml:"minio_bucket"`
}

func main() {
	logger := logging.New("replay-engine", slog.LevelInfo)
	if err := run(logger); err != nil {
		logger.Error("replay-engine exited with error", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg := serviceConfig{
		HTTPAddr:           ":8090",
		ClickHouseAddr:     "localhost:9000",
		ClickHouseUser:     "default",
		ClickHousePassword: "agentmesh",
		PostgresDSN:        "postgres://agentmesh:agentmesh@localhost:15432/agentmesh",
		APIKeyCacheTTLSecs: 60,
		MinIOAddr:          "localhost:9002",
		MinIOAccessKey:     "agentmesh",
		MinIOSecretKey:     "agentmesh-dev-secret",
		MinIOBucket:        "agentmesh-blobs",
	}
	if err := config.NewLoader().Load(&cfg, os.Getenv("AGENTMESH_CONFIG_FILE")); err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	chConn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{cfg.ClickHouseAddr},
		Auth: clickhouse.Auth{
			Username: cfg.ClickHouseUser,
			Password: cfg.ClickHousePassword,
		},
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

	minioClient, err := minio.New(cfg.MinIOAddr, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.MinIOAccessKey, cfg.MinIOSecretKey, ""),
		Secure: false,
	})
	if err != nil {
		return fmt.Errorf("creating minio client: %w", err)
	}
	blobClient := store.NewBlobStoreClient(minioClient, cfg.MinIOBucket)
	logger.Info("connected to minio", slog.String("bucket", cfg.MinIOBucket))

	authStore := authkeys.NewCachedStore(
		authkeys.NewPostgresStore(pgPool),
		time.Duration(cfg.APIKeyCacheTTLSecs)*time.Second,
	)

	spanReader := store.NewClickHouseSpanReader(chConn)
	runStore := store.NewReplayRunStore(pgPool)
	trajectoryReader := trajectory.NewReader(spanReader, blobClient)
	runner := execution.NewRunner(spanReader, blobClient, runStore)

	replayHandler := rest.NewReplayHandler(trajectoryReader, runner, authz.ProjectIDFromRequest)
	lookupHandler := rest.NewLookupHandler(runner)

	mux := http.NewServeMux()
	mux.Handle("/v1/replay/", lookupOrReplay(lookupHandler, authz.Middleware(authStore)(replayHandler)))
	mux.Handle("/v1/replay", authz.Middleware(authStore)(replayHandler))

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           rest.CORSMiddleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("replay-engine listening", slog.String("addr", cfg.HTTPAddr))
		serveErr <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received, stopping gracefully")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	}
}

// lookupOrReplay dispatches "/v1/replay/{id}/lookup" (unauthenticated,
// SDK-facing) separately from "/v1/replay/{id}/complete" (API-key
// authenticated, CLI-facing) — both share the "/v1/replay/" path prefix
// but need different auth treatment, per internal/rest.LookupHandler's
// doc comment on why the lookup path is intentionally open.
func lookupOrReplay(lookup http.Handler, authenticatedReplay http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Path) >= 7 && r.URL.Path[len(r.URL.Path)-7:] == "/lookup" {
			lookup.ServeHTTP(w, r)
			return
		}
		authenticatedReplay.ServeHTTP(w, r)
	})
}
