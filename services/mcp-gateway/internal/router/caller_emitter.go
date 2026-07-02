package router

import (
	"context"

	"github.com/agentmesh/agentmesh/services/mcp-gateway/internal/audit"
	"github.com/agentmesh/agentmesh/services/mcp-gateway/internal/proxy"
)

// callerAttributingEmitter decorates a proxy.AuditEmitter, stamping the
// OAuth-authenticated caller's identity (the bearer token's caller_name)
// onto audit.Call.CallerRecord before forwarding to the real emitter.
//
// audit.Call already carries a CallerRecord field ("free-form caller
// identity for the audit trail"), but proxy.AuditingInterceptor — which
// actually constructs each Call — only ever populates it from
// authz.ProjectIDFromContext's project, since that's all it knew about
// before per-server OAuth existed. Reaching into AuditingInterceptor to
// teach it about the new per-server caller identity is out of this
// change's scope (it's existing logic in another package's file); instead
// this decorates the AuditEmitter dependency AuditingInterceptor already
// accepts as an injected interface — exactly the seam that interface
// exists for — so no existing file's logic changes at all.
type callerAttributingEmitter struct {
	inner      proxy.AuditEmitter
	callerName string
}

// Emit implements proxy.AuditEmitter.
func (e callerAttributingEmitter) Emit(ctx context.Context, call audit.Call) error {
	if call.CallerRecord == "" {
		call.CallerRecord = e.callerName
	}
	return e.inner.Emit(ctx, call)
}
