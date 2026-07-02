package proxy

import (
	"context"
	"encoding/json"

	"github.com/agentmesh/agentmesh/services/mcp-gateway/internal/policy"
)

// mcpToolsCallMethod is the MCP JSON-RPC method name for a tool
// invocation, per docs/plan/MCP_Gateway_Architecture.md §3.2. Guardrails
// only apply to this method — any other MCP method (e.g. "resources/read",
// "initialize") passes through untouched, since the policy DSL is scoped
// to tool arguments.
const mcpToolsCallMethod = "tools/call"

// toolCallParams is the JSON shape of a "tools/call" request's params
// field, per the MCP spec.
type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// PolicyInterceptor adapts a *policy.Engine to the proxy's Interceptor
// interface: it extracts the tool name/arguments from a "tools/call"
// request and evaluates them against the loaded guardrail policies. This
// is the concrete wiring point named in
// docs/plan/MCP_Gateway_Architecture.md's implementation step 5
// ("Enforcement: Connect the policy engine to the interceptor to actively
// block requests").
type PolicyInterceptor struct {
	engine *policy.Engine
}

// NewPolicyInterceptor returns an Interceptor backed by engine.
func NewPolicyInterceptor(engine *policy.Engine) *PolicyInterceptor {
	return &PolicyInterceptor{engine: engine}
}

// Intercept implements Interceptor. Non-"tools/call" methods and
// malformed/empty params are never blocked here — a guardrail engine that
// fails open on unparseable input (rather than a hard proxy error) keeps
// non-tool MCP traffic (initialize, resources/list, ...) working even
// though this interceptor's scope is deliberately narrow to tool calls.
func (p *PolicyInterceptor) Intercept(ctx context.Context, req MCPRPCRequest) error {
	if req.Method != mcpToolsCallMethod {
		return nil
	}
	if len(req.Params) == 0 {
		return nil
	}

	var params toolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		// Malformed params for a tools/call request: not our job to
		// reject malformed JSON-RPC — the upstream MCP server's own
		// validation handles that. We only guard well-formed calls.
		return nil
	}

	return p.engine.Evaluate(policy.ToolCall{
		Name:      params.Name,
		Arguments: params.Arguments,
	})
}
