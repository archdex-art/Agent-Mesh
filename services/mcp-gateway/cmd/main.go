// Command mcp-gateway runs the AgentMesh MCP Security Gateway: an HTTP
// reverse proxy that authenticates callers, evaluates their MCP
// "tools/call" requests against declarative guardrail policies, and audits
// every decision to the Collector as an mcp.call span
// (docs/plan/MCP_Gateway_Architecture.md).
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

	"github.com/agentmesh/agentmesh/services/mcp-gateway/internal/audit"
	"github.com/agentmesh/agentmesh/services/mcp-gateway/internal/authz"
	"github.com/agentmesh/agentmesh/services/mcp-gateway/internal/oauth"
	"github.com/agentmesh/agentmesh/services/mcp-gateway/internal/policy"
	"github.com/agentmesh/agentmesh/services/mcp-gateway/internal/proxy"
	"github.com/agentmesh/agentmesh/services/mcp-gateway/internal/ratelimit"
	"github.com/agentmesh/agentmesh/services/mcp-gateway/internal/registry"
	"github.com/agentmesh/agentmesh/services/mcp-gateway/internal/router"
	"github.com/agentmesh/agentmesh/shared/authkeys"
	"github.com/agentmesh/agentmesh/shared/config"
	"github.com/agentmesh/agentmesh/shared/logging"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// serviceConfig is the Gateway's configuration surface, loaded per
// shared/config's env-over-yaml-over-default precedence (Architecture.md
// §12). Field defaults match the Docker Compose profile so `go run ./cmd`
// against a locally running compose stack works with zero configuration.
type serviceConfig struct {
	HTTPAddr           string `env:"AGENTMESH_MCPGATEWAY_HTTP_ADDR" yaml:"http_addr"`
	UpstreamMCPURL     string `env:"AGENTMESH_MCPGATEWAY_UPSTREAM_URL" yaml:"upstream_mcp_url"`
	PolicyFile         string `env:"AGENTMESH_MCPGATEWAY_POLICY_FILE" yaml:"policy_file"`
	CollectorAddr      string `env:"AGENTMESH_COLLECTOR_GRPC_ADDR" yaml:"collector_grpc_addr"`
	GatewayAPIKey      string `env:"AGENTMESH_MCPGATEWAY_API_KEY" yaml:"gateway_api_key"`
	PostgresDSN        string `env:"AGENTMESH_POSTGRES_DSN" yaml:"postgres_dsn"`
	APIKeyCacheTTLSecs int    `env:"AGENTMESH_APIKEY_CACHE_TTL_SECONDS" yaml:"apikey_cache_ttl_seconds"`
	RedisAddr          string `env:"AGENTMESH_REDIS_ADDR" yaml:"redis_addr"`
}

func main() {
	logger := logging.New("mcp-gateway", slog.LevelInfo)
	if err := run(logger); err != nil {
		logger.Error("mcp-gateway exited with error", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg := serviceConfig{
		HTTPAddr:           ":8090",
		UpstreamMCPURL:     "http://localhost:9090",
		PolicyFile:         "",
		CollectorAddr:      "localhost:4317",
		PostgresDSN:        "postgres://agentmesh:agentmesh@localhost:15432/agentmesh",
		APIKeyCacheTTLSecs: 60,
		RedisAddr:          "localhost:16379", // matches deploy/docker-compose.yml's redis port mapping
	}
	if err := config.NewLoader().Load(&cfg, os.Getenv("AGENTMESH_CONFIG_FILE")); err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if cfg.GatewayAPIKey == "" {
		return fmt.Errorf("AGENTMESH_MCPGATEWAY_API_KEY is required (the gateway authenticates its own audit-span exports to the Collector)")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

	// The per-server registry route's rate limiting is best-effort
	// infrastructure, mirroring services/collector/cmd/main.go's Redis
	// wiring exactly: a Redis connection failure here logs a warning and
	// disables rate limiting (nil *ratelimit.Limiter, which
	// router.Router treats as "never limit") rather than failing Gateway
	// startup, since guardrail enforcement and the legacy single-upstream
	// mode must keep working even if Redis is unavailable.
	redisClient := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	if err := redisClient.Ping(ctx).Err(); err != nil {
		logger.Warn("redis unavailable, mcp registry rate limiting disabled", slog.String("addr", cfg.RedisAddr), slog.Any("err", err))
		redisClient = nil
	} else {
		logger.Info("connected to redis", slog.String("addr", cfg.RedisAddr))
	}
	var limiter *ratelimit.Limiter
	if redisClient != nil {
		limiter = ratelimit.New(redisClient)
	}

	var policyEngine *policy.Engine
	if cfg.PolicyFile != "" {
		policyEngine, err = policy.LoadFile(cfg.PolicyFile)
		if err != nil {
			return fmt.Errorf("loading guardrail policy file %s: %w", cfg.PolicyFile, err)
		}
		logger.Info("loaded guardrail policies", slog.String("file", cfg.PolicyFile))
	} else {
		// No policies configured: an empty engine allows everything,
		// matching an explicit "no guardrails configured" deployment
		// rather than refusing to start.
		policyEngine, err = policy.Load([]byte("policies: []"))
		if err != nil {
			return fmt.Errorf("initializing empty policy engine: %w", err)
		}
		logger.Warn("no AGENTMESH_MCPGATEWAY_POLICY_FILE configured; running with zero guardrails")
	}

	emitter, err := audit.NewEmitter(cfg.CollectorAddr, cfg.GatewayAPIKey)
	if err != nil {
		return fmt.Errorf("creating audit emitter: %w", err)
	}
	defer emitter.Close()

	policyInterceptor := proxy.NewPolicyInterceptor(policyEngine)
	auditingInterceptor := proxy.NewAuditingInterceptor(policyInterceptor, emitter)

	gw, err := proxy.NewGateway(cfg.UpstreamMCPURL, auditingInterceptor)
	if err != nil {
		return fmt.Errorf("creating gateway proxy: %w", err)
	}

	// Legacy single-hardcoded-upstream mode: unchanged from before
	// Milestone 6, kept working for whatever already depends on it.
	legacyHandler := authz.Middleware(authStore)(gw)

	// Milestone 6's per-registered-server routing path: resolves
	// {server_name} against Postgres instead of using a single static
	// upstream, and additionally requires a per-server OAuth bearer
	// token on top of the same AgentMesh API key check (Architecture.md
	// §13). authz.Middleware wraps it exactly as it wraps legacyHandler
	// above — the AgentMesh-API-key layer is identical either way; only
	// what happens after it differs.
	registryStore := registry.NewPostgresStore(pgPool)
	oauthStore := oauth.NewPostgresStore(pgPool)
	mcpRouter := router.New(registryStore, oauthStore, limiter, emitter, policyEngine)
	registryHandler := authz.Middleware(authStore)(mcpRouter)

	mux := http.NewServeMux()
	mux.Handle(router.RoutePrefix, registryHandler)
	mux.Handle("/", legacyHandler)

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("mcp-gateway listening", slog.String("addr", cfg.HTTPAddr), slog.String("upstream", cfg.UpstreamMCPURL))
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
