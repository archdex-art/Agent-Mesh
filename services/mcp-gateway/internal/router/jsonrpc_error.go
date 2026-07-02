package router

import (
	"encoding/json"
	"net/http"
)

// JSON-RPC error codes this router can produce. -32000 through -32099 is
// the range the JSON-RPC 2.0 spec reserves for "implementation-defined
// server errors" — proxy.go already claims -32000 ("blocked by
// guardrail") and authz/middleware.go already claims -32001
// ("unauthenticated", at the AgentMesh-API-key layer); this router reuses
// -32001 for the per-server OAuth bearer-token layer (the assignment's
// documented convention: both auth layers fail the same way from a
// caller's point of view, a JSON-RPC "you are not authenticated" error,
// even though they authenticate different things) and claims two new
// codes of its own.
const (
	// codeInvalidRequest is JSON-RPC's own reserved "Invalid Request"
	// code, used for requests this router can reject before any
	// server/auth/rate-limit lookup even happens — wrong HTTP method or
	// a path missing the {server_name} segment entirely.
	codeInvalidRequest = -32600
	// codeUnauthenticated mirrors authz/middleware.go's writeUnauthenticated
	// exactly, reused here for the per-server bearer-token check: missing,
	// malformed, unknown, or revoked tokens are all indistinguishable to
	// the caller (anti-enumeration, matching oauth.Authenticate's
	// collapsing behavior).
	codeUnauthenticated = -32001
	// codeRateLimited is returned when the caller has exceeded the
	// server's configured requests_per_minute within the current window.
	codeRateLimited = -32002
	// codeServerNotFound is returned when {server_name} does not resolve
	// to a registered mcp_servers row for the caller's project (including
	// a server registered under a *different* project — see
	// registry.Store's doc comment on why that must look identical to
	// "does not exist").
	codeServerNotFound = -32003
	// codeInternal is JSON-RPC's own reserved "Internal error" code,
	// used for failures on our side that aren't the caller's fault
	// (Postgres/Redis unavailable, a guardrail document that failed to
	// compile) — deliberately distinct from the Gateway-specific codes
	// above so a caller can tell "you did something wrong" apart from
	// "we broke."
	codeInternal = -32603
)

// jsonRPCErrorResponse mirrors proxy.MCPRPCResponse's wire shape (this
// package cannot reuse that unexported-field-adjacent type directly
// without creating a router->proxy type dependency for no benefit, so it
// re-declares the same three JSON-RPC fields).
type jsonRPCErrorResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Error   jsonRPCErrorObj `json:"error"`
}

type jsonRPCErrorObj struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// writeJSONRPCError writes a JSON-RPC error response for a failure this
// router detected before ever handing the request to a per-server
// proxy.Gateway (unknown server, failed auth, rate limit, internal
// error) — every one of these happens before the request body is parsed
// as JSON-RPC, so id is always null, exactly like
// authz/middleware.go's writeUnauthenticated. The HTTP status stays 200:
// JSON-RPC transport convention (matching proxy.writeRPCError and
// authz.writeUnauthenticated) is that a JSON-RPC client reads the body,
// not the HTTP status, to determine success or failure — never a bare
// HTTP 401/404/429 for this reason.
func writeJSONRPCError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	resp := jsonRPCErrorResponse{
		JSONRPC: "2.0",
		ID:      nil,
		Error:   jsonRPCErrorObj{Code: code, Message: message},
	}
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}
