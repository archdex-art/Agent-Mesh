package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/agentmesh/agentmesh/shared/logging"
)

// MCPRPCRequest represents the relevant fields of an MCP JSON-RPC payload.
type MCPRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// MCPRPCError represents a JSON-RPC error response.
type MCPRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// MCPRPCResponse represents a JSON-RPC response payload.
type MCPRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Error   *MCPRPCError    `json:"error,omitempty"`
}

// Interceptor is an interface for evaluating and potentially blocking
// an MCP JSON-RPC request before it is forwarded.
type Interceptor interface {
	// Intercept examines the request. If it returns an error, the proxy
	// will halt forwarding and return a JSON-RPC error response containing
	// the error message.
	Intercept(ctx context.Context, req MCPRPCRequest) error
}

// Gateway is an HTTP reverse proxy that intercepts and inspects MCP
// JSON-RPC requests.
type Gateway struct {
	upstream    *url.URL
	interceptor Interceptor
	proxy       *httputil.ReverseProxy
}

// NewGateway creates a new MCP Gateway forwarding traffic to upstreamURL.
// The interceptor is called for every parsed JSON-RPC request.
func NewGateway(upstreamURL string, interceptor Interceptor) (*Gateway, error) {
	u, err := url.Parse(upstreamURL)
	if err != nil {
		return nil, fmt.Errorf("parsing upstream url: %w", err)
	}

	gw := &Gateway{
		upstream:    u,
		interceptor: interceptor,
	}

	proxy := httputil.NewSingleHostReverseProxy(u)
	
	// We preserve the original Director logic from SingleHostReverseProxy
	// but we don't need to modify it heavily here, as interception happens
	// at the Handler level before the proxy is invoked.
	gw.proxy = proxy
	return gw, nil
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	logger := logging.FromContext(r.Context())

	// MCP over HTTP typically uses POST for JSON-RPC messages.
	if r.Method == http.MethodPost {
		// Read the body so we can inspect it, then restore it so the proxy can forward it.
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			logger.Error("failed to read request body", slog.Any("error", err))
			writeRPCError(w, nil, -32700, "Parse error")
			return
		}
		r.Body.Close()
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		var rpcReq MCPRPCRequest
		if err := json.Unmarshal(bodyBytes, &rpcReq); err != nil {
			logger.Warn("failed to parse JSON-RPC request", slog.Any("error", err))
			// We don't block malformed JSON here; let the upstream server handle it.
			// But we can't intercept it.
		} else {
			// It's a valid JSON-RPC request. Run it through the interceptor.
			if err := g.interceptor.Intercept(r.Context(), rpcReq); err != nil {
				logger.Warn("request blocked by interceptor", slog.String("method", rpcReq.Method), slog.Any("reason", err))
				writeRPCError(w, rpcReq.ID, -32000, fmt.Sprintf("Blocked by Guardrail: %v", err))
				return
			}
		}
	}

	// Request allowed. Forward to upstream.
	g.proxy.ServeHTTP(w, r)
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK) // JSON-RPC errors are usually 200 OK at the HTTP transport layer

	resp := MCPRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &MCPRPCError{
			Code:    code,
			Message: message,
		},
	}
	
	// If ID is null/missing, ensure it outputs as literally `null` in JSON per spec
	if len(id) == 0 {
		resp.ID = json.RawMessage(`null`)
	}

	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}
