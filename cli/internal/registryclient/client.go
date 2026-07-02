// Package registryclient talks to the Query API's MCP Registry REST
// surface (Architecture.md §10: "agentmesh mcp register <manifest>
// --gateway-url <url> — registers a server and prints the Gateway URL
// to swap into the agent's config"). Like cli/internal/tailclient, this
// package deliberately declares its own request/response wire types
// rather than importing any server-side module: the CLI is a separately
// distributed binary that only depends on the documented HTTP contract
// (docs/plan/Milestones.md's Milestone 6 Registry surface), not on the
// Query API's internal Go types — the same "wire contract, not shared
// Go types" boundary tailclient.go documents for the Realtime Gateway.
package registryclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// apiKeyHeader is the HTTP header every AgentMesh HTTP endpoint expects
// the caller's raw API key on. It mirrors
// services/mcp-gateway/internal/authz.APIKeyHeader and
// services/query-api/internal/authz's unexported equivalent exactly —
// duplicated here rather than imported for the same cross-binary
// independence reason as the wire types above.
const apiKeyHeader = "X-AgentMesh-API-Key"

// httpClient is package-level so `agentmesh mcp register` never hangs
// indefinitely against an unreachable Query API.
var httpClient = &http.Client{Timeout: 15 * time.Second}

// RegisterRequest is the JSON body for POST /v1/mcp/servers, matching
// the Query API's documented Registry contract exactly. RuleDSL is
// optional: an empty string registers the server with no guardrail
// policy (the Gateway's existing "empty engine allows everything"
// fallback then applies).
type RegisterRequest struct {
	Name         string `json:"name"`
	UpstreamURL  string `json:"upstream_url"`
	Transport    string `json:"transport"`
	Version      string `json:"version"`
	Owner        string `json:"owner"`
	ManifestYAML string `json:"manifest_yaml"`
	RuleDSL      string `json:"rule_dsl,omitempty"`
}

// RegisterResponse is the subset of the Query API's 201 response body
// the CLI needs to confirm registration and print the Gateway URL.
type RegisterResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Register POSTs req as JSON to queryAPIURL's /v1/mcp/servers endpoint,
// authenticating with apiKey via the standard AgentMesh API-key header.
// On any non-2xx response, the error message includes the response body
// verbatim (truncated) so a failed `agentmesh mcp register` is
// debuggable from the terminal alone — e.g. a 409 from the DB's
// UNIQUE(project_id, name) constraint on mcp_servers.
func Register(queryAPIURL, apiKey string, req RegisterRequest) (RegisterResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return RegisterResponse{}, fmt.Errorf("encoding register request: %w", err)
	}

	endpoint := strings.TrimRight(queryAPIURL, "/") + "/v1/mcp/servers"
	httpReq, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return RegisterResponse{}, fmt.Errorf("building request to %s: %w", endpoint, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(apiKeyHeader, apiKey)

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return RegisterResponse{}, fmt.Errorf("calling Registry at %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return RegisterResponse{}, fmt.Errorf("reading Registry response from %s: %w", endpoint, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return RegisterResponse{}, fmt.Errorf("Registry rejected registration (status %d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var out RegisterResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return RegisterResponse{}, fmt.Errorf("decoding Registry response from %s: %w (body: %s)", endpoint, err, strings.TrimSpace(string(respBody)))
	}
	return out, nil
}
