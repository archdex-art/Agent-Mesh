package proxy

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/agentmesh/agentmesh/services/mcp-gateway/internal/audit"
	"github.com/agentmesh/agentmesh/services/mcp-gateway/internal/authz"
	"github.com/agentmesh/agentmesh/shared/logging"
	"github.com/agentmesh/agentmesh/shared/span"
)

// AuditEmitter is the minimal dependency AuditingInterceptor needs to send
// an audit span. Declared as an interface (rather than importing
// *audit.Emitter concretely everywhere it's used) so tests can substitute
// a fake without a live gRPC connection to a Collector — Phase 3's
// "independently testable" standard.
type AuditEmitter interface {
	Emit(ctx context.Context, call audit.Call) error
}

// AuditingInterceptor wraps another Interceptor, emitting one mcp.call
// OTLP span to the Collector for every "tools/call" request it sees —
// allowed or denied — per docs/plan/MCP_Gateway_Architecture.md's sequence
// diagram ("Emit mcp.call span" on both the allow and deny branches) and
// Architecture.md §3's KindMCPCall definition.
type AuditingInterceptor struct {
	inner   Interceptor
	emitter AuditEmitter
}

// NewAuditingInterceptor wraps inner, auditing every tools/call request
// through emitter.
func NewAuditingInterceptor(inner Interceptor, emitter AuditEmitter) *AuditingInterceptor {
	return &AuditingInterceptor{inner: inner, emitter: emitter}
}

// Intercept implements Interceptor: it delegates to inner for the actual
// allow/deny decision, then emits an audit span reflecting that decision.
// A failure to emit the audit span never changes Intercept's return value
// — Architecture.md §17's ingestion-path philosophy ("losing a trace is
// recoverable, breaking the proxied call is not") applies here: audit
// emission is best-effort telemetry about the gateway, never a precondition
// for the gateway doing its actual job.
func (a *AuditingInterceptor) Intercept(ctx context.Context, req MCPRPCRequest) error {
	if req.Method != mcpToolsCallMethod {
		return a.inner.Intercept(ctx, req)
	}

	toolName := "unknown"
	if len(req.Params) > 0 {
		var params toolCallParams
		if err := json.Unmarshal(req.Params, &params); err == nil && params.Name != "" {
			toolName = params.Name
		}
	}

	start := time.Now()
	innerErr := a.inner.Intercept(ctx, req)
	end := time.Now()

	projectID, pidErr := authz.ProjectIDFromContext(ctx)
	if pidErr != nil {
		// No authenticated project on this request (e.g. auth middleware
		// wasn't wired ahead of this interceptor in a given deployment) —
		// we cannot scope an audit span without a project, so we skip
		// emission rather than emit an unscoped/misattributed span.
		logging.FromContext(ctx).Warn("skipping mcp.call audit emission: no project id in context", slog.Any("error", pidErr))
		return innerErr
	}

	status := span.StatusOK
	denyReason := ""
	if innerErr != nil {
		status = span.StatusDenied
		denyReason = innerErr.Error()
	}

	call := audit.Call{
		ProjectID:  projectID,
		ToolName:   toolName,
		Status:     status,
		DenyReason: denyReason,
		StartTime:  start,
		EndTime:    end,
	}

	if emitErr := a.emitter.Emit(ctx, call); emitErr != nil {
		logging.FromContext(ctx).Warn("failed to emit mcp.call audit span", slog.Any("error", emitErr))
	}

	return innerErr
}
