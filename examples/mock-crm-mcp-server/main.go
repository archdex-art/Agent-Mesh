// Command mock-crm-mcp-server is Milestone 6's demo upstream: "a simple
// mock CRM tool" (docs/plan/Milestones.md's Milestone 6 success
// criteria) that an agent calls through the MCP Gateway instead of
// directly, proving out registration + Gateway routing + auth +
// guardrails + audit end to end.
//
// It is intentionally the smallest thing that can pass for an MCP
// server: stdlib net/http only (no SDK, no framework — matching
// examples/shared's dependency-light spirit for the Python examples),
// implementing just enough of MCP's JSON-RPC 2.0 wire shape
// (docs/plan/MCP_Gateway_Architecture.md §3.2) to answer an
// "initialize" handshake and a "tools/call" request for one fake tool,
// lookup_customer. It has no auth, no guardrails, and no persistence of
// its own — all of that governance is exactly what the Gateway sitting
// in front of it (services/mcp-gateway) is responsible for adding.
package main

import (
	"encoding/json"
	"flag"
	"io"
	"log/slog"
	"net/http"
	"os"
)

// rpcRequest is the subset of a JSON-RPC 2.0 request this server reads.
// It mirrors services/mcp-gateway/internal/proxy.MCPRPCRequest's shape
// (independently declared, not imported — this binary has no
// compile-time dependency on any AgentMesh service, the same "wire
// contract, not shared Go types" boundary cli/internal/tailclient
// documents).
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// customerRecord is the canned "database" this mock CRM serves —
// enough to make the demo call visibly return real-looking data instead
// of an empty stub.
var customerRecord = map[string]any{
	"customer": "Acme Corp",
	"status":   "active",
}

func main() {
	addr := flag.String("addr", ":9090", "listen address (default matches the Gateway's UpstreamMCPURL of http://localhost:9090)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil)).With(slog.String("service", "mock-crm-mcp-server"))

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRPC(logger))

	logger.Info("mock-crm-mcp-server listening", slog.String("addr", *addr))
	if err := http.ListenAndServe(*addr, mux); err != nil {
		logger.Error("server exited", slog.Any("error", err))
		os.Exit(1)
	}
}

func handleRPC(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed: MCP JSON-RPC requests are POST", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeRPC(w, nil, nil, &rpcError{Code: -32700, Message: "Parse error: " + err.Error()})
			return
		}

		var req rpcRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeRPC(w, nil, nil, &rpcError{Code: -32700, Message: "Parse error: " + err.Error()})
			return
		}

		switch req.Method {
		case "initialize":
			// The minimal handshake result shape an MCP client needs to
			// consider the connection ready before issuing tool calls.
			writeRPC(w, req.ID, map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities": map[string]any{
					"tools": map[string]any{},
				},
				"serverInfo": map[string]any{
					"name":    "mock-crm-mcp-server",
					"version": "0.1.0",
				},
			}, nil)

		case "tools/call":
			handleToolsCall(w, logger, req)

		default:
			logger.Warn("unrecognized method", slog.String("method", req.Method))
			writeRPC(w, req.ID, nil, &rpcError{Code: -32601, Message: "Method not found: " + req.Method})
		}
	}
}

func handleToolsCall(w http.ResponseWriter, logger *slog.Logger, req rpcRequest) {
	var params toolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeRPC(w, req.ID, nil, &rpcError{Code: -32602, Message: "Invalid params: " + err.Error()})
		return
	}

	if params.Name != "lookup_customer" {
		logger.Warn("unknown tool requested", slog.String("tool", params.Name))
		writeRPC(w, req.ID, nil, &rpcError{Code: -32601, Message: "Unknown tool: " + params.Name})
		return
	}

	logger.Info("lookup_customer called", slog.String("arguments", string(params.Arguments)))

	// Real MCP servers wrap tool results in a `content` block of typed
	// parts; a single JSON-encoded text part is the simplest shape a
	// generic MCP client can render, so the canned CRM payload rides
	// inside one.
	payload, _ := json.Marshal(customerRecord)
	writeRPC(w, req.ID, map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": string(payload)},
		},
		"isError": false,
	}, nil)
}

func writeRPC(w http.ResponseWriter, id json.RawMessage, result any, rpcErr *rpcError) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK) // JSON-RPC transport-level status is 200 even for RPC-level errors.
	_ = json.NewEncoder(w).Encode(rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
		Error:   rpcErr,
	})
}
