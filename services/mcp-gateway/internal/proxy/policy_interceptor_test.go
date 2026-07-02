package proxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentmesh/agentmesh/services/mcp-gateway/internal/policy"
)

const destructiveSQLPolicyYAML = `
policies:
  - name: prevent_destructive_sql
    target_tools: ["execute_query"]
    action: deny
    rules:
      - param_matches:
          param: "sql"
          pattern: "(?i)(DROP|DELETE|TRUNCATE|ALTER)"
`

func mustLoadPolicy(t *testing.T, yamlDoc string) *policy.Engine {
	t.Helper()
	engine, err := policy.Load([]byte(yamlDoc))
	if err != nil {
		t.Fatalf("policy.Load: %v", err)
	}
	return engine
}

// TestFullChainBlocksRealMaliciousMCPRequest is the end-to-end proof named
// in the build plan: a real YAML guardrail policy, wired through the real
// PolicyInterceptor, into the real proxy — and a genuinely malicious
// "tools/call" (DROP TABLE) request is blocked before ever reaching the
// upstream MCP server.
func TestFullChainBlocksRealMaliciousMCPRequest(t *testing.T) {
	upstreamHit := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit = true
	}))
	defer upstream.Close()

	engine := mustLoadPolicy(t, destructiveSQLPolicyYAML)
	interceptor := NewPolicyInterceptor(engine)
	gw, err := NewGateway(upstream.URL, interceptor)
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}

	reqBody := `{
		"jsonrpc": "2.0",
		"id": 7,
		"method": "tools/call",
		"params": {
			"name": "execute_query",
			"arguments": {"sql": "DROP TABLE users;"}
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	gw.ServeHTTP(rec, req)

	if upstreamHit {
		t.Fatal("upstream MCP server was hit despite a malicious DROP TABLE payload — guardrail did not block")
	}

	var resp MCPRPCResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON-RPC: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected a JSON-RPC error response for the blocked request, got none")
	}
	if resp.Error.Code != -32000 {
		t.Fatalf("error.code = %d, want -32000", resp.Error.Code)
	}
}

// TestFullChainAllowsRealSafeMCPRequest proves the inverse: a legitimate
// SELECT query for the same tool passes through untouched.
func TestFullChainAllowsRealSafeMCPRequest(t *testing.T) {
	upstreamHit := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit = true
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"jsonrpc": "2.0", "id": 8, "result": {"rows": []}}`))
	}))
	defer upstream.Close()

	engine := mustLoadPolicy(t, destructiveSQLPolicyYAML)
	interceptor := NewPolicyInterceptor(engine)
	gw, err := NewGateway(upstream.URL, interceptor)
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}

	reqBody := `{
		"jsonrpc": "2.0",
		"id": 8,
		"method": "tools/call",
		"params": {
			"name": "execute_query",
			"arguments": {"sql": "SELECT * FROM users;"}
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	gw.ServeHTTP(rec, req)

	if !upstreamHit {
		t.Fatal("upstream MCP server was not hit for a legitimate SELECT query")
	}
}

// TestFullChainPassesThroughNonToolCallMethods proves non-"tools/call"
// MCP methods (e.g. initialize) are never evaluated against guardrails.
func TestFullChainPassesThroughNonToolCallMethods(t *testing.T) {
	upstreamHit := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	engine := mustLoadPolicy(t, destructiveSQLPolicyYAML)
	interceptor := NewPolicyInterceptor(engine)
	gw, err := NewGateway(upstream.URL, interceptor)
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}

	reqBody := `{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {}}`
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(reqBody))
	rec := httptest.NewRecorder()

	gw.ServeHTTP(rec, req)

	if !upstreamHit {
		t.Fatal("non-tools/call method was blocked, want pass-through")
	}
}

