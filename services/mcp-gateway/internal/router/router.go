// Package router implements the MCP Gateway's per-registered-server
// routing path: `POST /v1/mcp/{server_name}/...`, the Milestone 6
// counterpart to the Gateway's existing single-hardcoded-upstream mode
// (services/mcp-gateway/internal/proxy, still wired unchanged in
// cmd/main.go at its own route for backward compatibility).
//
// docs/plan/Architecture.md §13 describes three things this path adds on
// top of the legacy mode, and this file implements exactly those three,
// per request, in order:
//
//  1. Resolve {server_name} to a registered mcp_servers row, scoped to
//     the caller's AgentMesh project (registry.Store) — "the Gateway
//     routes to many registered servers, not one static upstream."
//  2. Authenticate the caller to *that server* via an OAuth
//     2.1-style opaque bearer token (oauth.Store/Authenticate),
//     independent of the AgentMesh API key that got the request this
//     far — "a caller authenticates to the tool, not to AgentMesh."
//  3. Rate-limit per (server, caller) if the server's guardrail policy
//     document configures one (ratelimit.Limiter).
//
// It then builds the same policy/proxy/audit interceptor chain the
// legacy mode already uses — reused verbatim, not reimplemented — around
// that specific server's upstream URL and guardrail policy, and forwards
// the request.
package router

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/agentmesh/agentmesh/services/mcp-gateway/internal/authz"
	"github.com/agentmesh/agentmesh/services/mcp-gateway/internal/oauth"
	"github.com/agentmesh/agentmesh/services/mcp-gateway/internal/policy"
	"github.com/agentmesh/agentmesh/services/mcp-gateway/internal/proxy"
	"github.com/agentmesh/agentmesh/services/mcp-gateway/internal/ratelimit"
	"github.com/agentmesh/agentmesh/services/mcp-gateway/internal/registry"
	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/logging"
)

// RoutePrefix is the mount point cmd/main.go registers this Router under
// (e.g. `mux.Handle(RoutePrefix, authz.Middleware(authStore)(r))`),
// exported so main.go and this package can never disagree on it.
const RoutePrefix = "/v1/mcp/"

// rateLimitWindow is fixed at one minute because the policy DSL's
// rate_limit field is itself denominated in requests_per_minute
// (services/mcp-gateway/internal/policy.RateLimit) — there is only one
// window size to pick given that unit.
const rateLimitWindow = time.Minute

// Router serves POST /v1/mcp/{server_name}/... by resolving server_name
// against registry, authenticating the caller against oauthStore, rate
// limiting via limiter (if configured and non-nil), and forwarding
// through the same guardrail-policy + audit interceptor chain the
// legacy single-upstream Gateway uses.
type Router struct {
	registry             registry.Store
	oauthStore           oauth.Store
	limiter              *ratelimit.Limiter // nil disables rate limiting entirely (e.g. Redis unavailable at startup) — every request behaves as if no server ever configured a rate_limit
	auditEmitter         proxy.AuditEmitter
	fallbackPolicyEngine *policy.Engine // used when a resolved server has no enabled guardrail_policies row; this is the Gateway's existing static-file-or-empty engine, so legacy behavior for policy-less servers is unchanged
}

// New returns a Router with the given dependencies. Every dependency is
// an interface (or, for limiter, a concrete type that itself wraps an
// interface-shaped Redis client) so unit tests can substitute fakes
// without live Postgres/Redis (Phase 3's "independently testable"
// standard).
func New(reg registry.Store, oauthStore oauth.Store, limiter *ratelimit.Limiter, auditEmitter proxy.AuditEmitter, fallbackPolicyEngine *policy.Engine) *Router {
	return &Router{
		registry:             reg,
		oauthStore:           oauthStore,
		limiter:              limiter,
		auditEmitter:         auditEmitter,
		fallbackPolicyEngine: fallbackPolicyEngine,
	}
}

// ServeHTTP implements http.Handler.
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logging.FromContext(ctx)

	if r.Method != http.MethodPost {
		writeJSONRPCError(w, codeInvalidRequest, "method not allowed: MCP requests are POST-only")
		return
	}

	serverName, subPath, ok := parseServerPath(r.URL.Path)
	if !ok {
		writeJSONRPCError(w, codeInvalidRequest, "missing MCP server name in path (expected "+RoutePrefix+"{server_name})")
		return
	}

	// authz.Middleware (wired around this Router in cmd/main.go, exactly
	// as it wraps the legacy proxy.Gateway today) has already validated
	// the caller's AgentMesh API key and stashed the resolved project by
	// the time ServeHTTP runs; reaching this point without one means the
	// Router was mounted without that middleware somewhere (a wiring
	// bug, not a caller error), so this fails the same closed way authz
	// itself would.
	projectID, err := authz.ProjectIDFromContext(ctx)
	if err != nil {
		logger.Error("router: no authenticated project in request context (authz.Middleware not applied?)", slog.Any("error", err))
		writeJSONRPCError(w, codeUnauthenticated, "Unauthenticated: missing or invalid API key")
		return
	}

	server, err := rt.registry.ResolveServer(ctx, projectID, serverName)
	if err != nil {
		if amerrors.CodeOf(err) == amerrors.CodeNotFound {
			writeJSONRPCError(w, codeServerNotFound, "no MCP server registered as \""+serverName+"\"")
			return
		}
		logger.Error("router: resolving mcp server", slog.String("server", serverName), slog.Any("error", err))
		writeJSONRPCError(w, codeInternal, "internal error resolving MCP server")
		return
	}

	tokenRecord, err := oauth.Authenticate(ctx, rt.oauthStore, bearerToken(r.Header.Get("Authorization")))
	if err != nil {
		writeJSONRPCError(w, codeUnauthenticated, "Unauthenticated: missing or invalid bearer token")
		return
	}
	if tokenRecord.MCPServerID != server.ID.String() {
		// The token is well-formed and unrevoked, but minted for a
		// different server. Respond identically to "no token supplied"
		// rather than confirming the token is valid for something else.
		writeJSONRPCError(w, codeUnauthenticated, "Unauthenticated: bearer token is not valid for this MCP server")
		return
	}

	engine, err := rt.resolvePolicyEngine(ctx, server)
	if err != nil {
		logger.Error("router: resolving guardrail policy", slog.String("server", serverName), slog.Any("error", err))
		writeJSONRPCError(w, codeInternal, "internal error loading guardrail policy")
		return
	}

	if allowed, err := rt.checkRateLimit(ctx, server, tokenRecord, engine); err != nil {
		logger.Warn("router: rate limiter unavailable, failing open", slog.String("server", serverName), slog.Any("error", err))
	} else if !allowed {
		writeJSONRPCError(w, codeRateLimited, "rate limit exceeded")
		return
	}

	policyInterceptor := proxy.NewPolicyInterceptor(engine)
	emitter := callerAttributingEmitter{inner: rt.auditEmitter, callerName: tokenRecord.CallerName}
	auditingInterceptor := proxy.NewAuditingInterceptor(policyInterceptor, emitter)

	// A fresh proxy.Gateway is built per request (not cached) so a
	// server's upstream_url/guardrail policy update in Postgres takes
	// effect on the very next call — a stale cached upstream after an
	// operator updates a server's URL is a real bug this deliberately
	// avoids; correctness over the marginal per-request construction
	// cost (parsing a URL and building a *httputil.ReverseProxy) in v1.
	gw, err := proxy.NewGateway(server.UpstreamURL, auditingInterceptor)
	if err != nil {
		logger.Error("router: constructing per-request gateway", slog.String("server", serverName), slog.Any("error", err))
		writeJSONRPCError(w, codeInternal, "internal error constructing upstream connection")
		return
	}

	r.URL.Path = subPath
	gw.ServeHTTP(w, r)
}

// resolvePolicyEngine loads server's enabled guardrail_policies row (if
// any) and compiles it via the existing policy.Load, falling back to
// fallbackPolicyEngine when the server has none — the Gateway's existing
// "static PolicyFile, or an empty engine that allows everything" default,
// unchanged for servers that were never given a per-server policy.
func (rt *Router) resolvePolicyEngine(ctx context.Context, server registry.Server) (*policy.Engine, error) {
	ruleDSL, ok, err := rt.registry.GuardrailPolicy(ctx, server.ID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return rt.fallbackPolicyEngine, nil
	}
	return policy.Load([]byte(ruleDSL))
}

// checkRateLimit applies engine's optional rate_limit configuration, if
// any, scoped to this specific server + caller. A nil limiter (Redis was
// unavailable at startup) or a nil/zero RateLimit (server never
// configured one) both mean "no limiting" — fail open on absence, per
// the Contract's documented default, distinct from failing open on a
// live Redis error, which this also does (logged by the caller) since a
// rate limiter is an anti-abuse guardrail, not a correctness-critical
// control gating whether the call happens at all.
func (rt *Router) checkRateLimit(ctx context.Context, server registry.Server, tokenRecord oauth.TokenRecord, engine *policy.Engine) (bool, error) {
	if rt.limiter == nil {
		return true, nil
	}
	rl := engine.RateLimit()
	if rl == nil || rl.RequestsPerMinute <= 0 {
		return true, nil
	}
	key := server.ID.String() + ":" + tokenRecord.CallerName
	return rt.limiter.Allow(ctx, key, rl.RequestsPerMinute, rateLimitWindow)
}

// parseServerPath splits a request path of the form
// "/v1/mcp/{server_name}" or "/v1/mcp/{server_name}/{rest...}" into the
// server name and the path to forward upstream. The upstream path
// defaults to "/" when the caller POSTs directly to
// "/v1/mcp/{server_name}" with no further segments — MCP's
// streamable-http transport commonly speaks JSON-RPC over a single
// endpoint, so a bare server-name path is a first-class shape, not an
// error.
func parseServerPath(urlPath string) (serverName, subPath string, ok bool) {
	rest := strings.TrimPrefix(urlPath, RoutePrefix)
	if rest == urlPath || rest == "" {
		return "", "", false
	}
	name, tail, found := strings.Cut(rest, "/")
	if name == "" {
		return "", "", false
	}
	if !found || tail == "" {
		return name, "/", true
	}
	return name, "/" + tail, true
}

// bearerToken extracts the raw token from an "Authorization: Bearer
// <token>" header, or "" if the header is absent or a different scheme —
// oauth.Authenticate treats "" the same as "no header at all" (missing
// bearer token), so this never needs to special-case "no Authorization
// header" itself.
func bearerToken(header string) string {
	const schemePrefix = "Bearer "
	if len(header) <= len(schemePrefix) {
		return ""
	}
	if !strings.EqualFold(header[:len(schemePrefix)], schemePrefix) {
		return ""
	}
	return header[len(schemePrefix):]
}
