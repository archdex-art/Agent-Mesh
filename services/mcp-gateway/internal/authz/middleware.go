// Package authz implements the MCP Gateway's caller authentication.
//
// Unlike the Query API's authz middleware (which returns a generic JSON
// error body), the Gateway's callers speak JSON-RPC — an unauthenticated
// request must still get back a well-formed JSON-RPC error object, not a
// bare HTTP 401 with an unrelated JSON shape, so a caller's JSON-RPC client
// library can parse the failure the same way it parses any other RPC
// error.
//
// This reuses the same authkeys.Authenticate entry point as the Collector
// and Query API (Architecture.md §13), so a single API key works
// identically across all three ingress points.
package authz

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/agentmesh/agentmesh/shared/authkeys"
	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
)

// APIKeyHeader is the HTTP header carrying the caller's raw API key.
const APIKeyHeader = "X-AgentMesh-API-Key"

type contextKey struct{}

// Middleware authenticates every request via APIKeyHeader and, on success,
// stashes the resolved authkeys.Record in the request context. On failure
// it fails closed (Architecture.md §17: the Gateway "fails closed for
// governed calls" rather than silently bypassing the policy it exists to
// enforce) and writes a JSON-RPC-shaped error so callers get a consistent
// error envelope regardless of whether auth or a guardrail rejected them.
func Middleware(store authkeys.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawKey := r.Header.Get(APIKeyHeader)
			record, err := authkeys.Authenticate(r.Context(), store, rawKey)
			if err != nil {
				writeUnauthenticated(w)
				return
			}
			ctx := context.WithValue(r.Context(), contextKey{}, record)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RecordFromContext retrieves the authkeys.Record stashed by Middleware.
func RecordFromContext(ctx context.Context) (authkeys.Record, error) {
	record, ok := ctx.Value(contextKey{}).(authkeys.Record)
	if !ok {
		return authkeys.Record{}, amerrors.New(amerrors.CodeInternal, "authz: no authenticated record in context (middleware not applied?)")
	}
	return record, nil
}

// ProjectIDFromContext extracts just the ProjectID, the piece the
// interceptor/audit path needs to scope emitted spans.
func ProjectIDFromContext(ctx context.Context) (ids.ProjectID, error) {
	record, err := RecordFromContext(ctx)
	if err != nil {
		return ids.ProjectID{}, err
	}
	return record.ProjectID, nil
}

func writeUnauthenticated(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	// JSON-RPC transport convention (matching proxy.writeRPCError): the
	// HTTP status stays 200 and the failure is carried in the JSON-RPC
	// error object, since a JSON-RPC client reads the body, not the HTTP
	// status, to determine success/failure.
	w.WriteHeader(http.StatusOK)
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      nil,
		"error": map[string]any{
			"code":    -32001,
			"message": "Unauthenticated: missing or invalid API key",
		},
	}
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}
