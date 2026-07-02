// Command collector runs the AgentMesh Collector service: an OTLP gRPC
// receiver that authenticates callers via API key, decodes spans per
// docs/otlp-mapping.md, and batch-writes them to ClickHouse
// (Architecture.md §2).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/agentmesh/agentmesh/services/collector/internal/blobstore"
	"github.com/agentmesh/agentmesh/services/collector/internal/ingest"
	"github.com/agentmesh/agentmesh/services/collector/internal/publisher"
	"github.com/agentmesh/agentmesh/services/collector/internal/writer"
	"github.com/agentmesh/agentmesh/shared/authkeys"
	"github.com/agentmesh/agentmesh/shared/config"
	"github.com/agentmesh/agentmesh/shared/logging"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/redis/go-redis/v9"
	collectorpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
)

// serviceConfig is the Collector's configuration surface, loaded per
// shared/config's env-over-yaml-over-default precedence (Architecture.md
// §12). Field defaults below match the Docker Compose profile in
// deploy/docker-compose.yml so `go run ./cmd` against a locally running
// compose stack works with zero configuration.
type serviceConfig struct {
	GRPCAddr           string `env:"AGENTMESH_COLLECTOR_GRPC_ADDR" yaml:"grpc_addr"`
	ClickHouseAddr     string `env:"AGENTMESH_CLICKHOUSE_ADDR" yaml:"clickhouse_addr"`
	ClickHouseUser     string `env:"AGENTMESH_CLICKHOUSE_USER" yaml:"clickhouse_user"`
	ClickHousePassword string `env:"AGENTMESH_CLICKHOUSE_PASSWORD" yaml:"clickhouse_password"`
	PostgresDSN        string `env:"AGENTMESH_POSTGRES_DSN" yaml:"postgres_dsn"`
	APIKeyCacheTTLSecs int    `env:"AGENTMESH_APIKEY_CACHE_TTL_SECONDS" yaml:"apikey_cache_ttl_seconds"`
	MinIOAddr          string `env:"AGENTMESH_MINIO_ADDR" yaml:"minio_addr"`
	MinIOAccessKey     string `env:"AGENTMESH_MINIO_ACCESS_KEY" yaml:"minio_access_key"`
	MinIOSecretKey     string `env:"AGENTMESH_MINIO_SECRET_KEY" yaml:"minio_secret_key"`
	MinIOBucket        string `env:"AGENTMESH_MINIO_BUCKET" yaml:"minio_bucket"`
	RedisAddr          string `env:"AGENTMESH_REDIS_ADDR" yaml:"redis_addr"`
}

func main() {
	logger := logging.New("collector", slog.LevelInfo)

	if err := run(logger); err != nil {
		logger.Error("collector exited with error", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg := serviceConfig{
		GRPCAddr:           ":4317", // OTLP's conventional gRPC port
		ClickHouseAddr:     "localhost:9000",
		ClickHouseUser:     "default",
		ClickHousePassword: "agentmesh", // matches deploy/docker-compose.yml's CLICKHOUSE_PASSWORD
		PostgresDSN:        "postgres://agentmesh:agentmesh@localhost:15432/agentmesh",
		APIKeyCacheTTLSecs: 60,
		MinIOAddr:          "localhost:9002", // matches deploy/docker-compose.yml's minio S3-API port mapping
		MinIOAccessKey:     "agentmesh",
		MinIOSecretKey:     "agentmesh-dev-secret",
		MinIOBucket:        "agentmesh-blobs",
		RedisAddr:          "localhost:16379", // matches deploy/docker-compose.yml's redis port mapping
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
	logger.Info("connected to clickhouse", slog.String("addr", cfg.ClickHouseAddr))

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
	minioClient, err := minio.New(cfg.MinIOAddr, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.MinIOAccessKey, cfg.MinIOSecretKey, ""),
		Secure: false,
	})
	if err != nil {
		return fmt.Errorf("creating minio client: %w", err)
	}
	blobClient := blobstore.New(minioClient, cfg.MinIOBucket)
	if err := blobClient.EnsureBucket(ctx); err != nil {
		return fmt.Errorf("ensuring minio bucket %q exists: %w", cfg.MinIOBucket, err)
	}
	logger.Info("connected to minio", slog.String("bucket", cfg.MinIOBucket))

	// Realtime fan-out is best-effort infrastructure (SpanPublisher's
	// documented contract): a Redis connection failure here logs a
	// warning and disables live-tailing rather than failing Collector
	// startup, since ingestion must keep working even if the Realtime
	// Gateway's dependency is unavailable.
	redisClient := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	if err := redisClient.Ping(ctx).Err(); err != nil {
		logger.Warn("redis unavailable, realtime fan-out disabled", slog.String("addr", cfg.RedisAddr), slog.Any("err", err))
		redisClient = nil
	} else {
		logger.Info("connected to redis", slog.String("addr", cfg.RedisAddr))
	}

	spanWriter := writer.New(chConn)
	decoder := ingest.NewDecoder()
	offloader := ingest.NewOffloader(blobClient)
	server := ingest.NewServer(authStore, decoder, offloader, spanWriter)
	if redisClient != nil {
		server.SetPublisher(publisher.New(redisClient, logger))
	}

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", cfg.GRPCAddr, err)
	}

	grpcServer := grpc.NewServer()
	collectorpb.RegisterTraceServiceServer(grpcServer, server)

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("collector listening", slog.String("addr", cfg.GRPCAddr))
		serveErr <- grpcServer.Serve(lis)
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received, stopping gracefully")
		grpcServer.GracefulStop()
		return nil
	case err := <-serveErr:
		if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			return fmt.Errorf("grpc server: %w", err)
		}
		return nil
	}
}
