// Package authz implements the Query API's HTTP-layer authentication:
// resolving the caller's ProjectID from an API key, shared with the
// Collector's ingestion-path auth via the same authkeys.Authenticate
// entry point (Architecture.md §13's "single entry point both the
// Collector's gRPC interceptor and the Query API's HTTP middleware call").
package authz

import (
	"context"
	"net/http"

	"github.com/agentmesh/agentmesh/shared/authkeys"
	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
)

// apiKeyHeader is the HTTP header carrying the caller's raw API key.
// Distinct constant from the Collector's gRPC metadata key
// (x-agentmesh-api-key) even though the value is the same convention,
// because HTTP and gRPC transports have independent header/metadata
// namespaces — this is not a duplication of the auth *logic* (that's
// shared via authkeys.Authenticate), only of the transport-specific header
// name.
const apiKeyHeader = "X-AgentMesh-API-Key"

type contextKey struct{}

// Middleware authenticates every request via apiKeyHeader and, on success,
// stashes the resolved authkeys.Record in the request context for
// downstream handlers to read via RecordFromContext.
func Middleware(store authkeys.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawKey := r.Header.Get(apiKeyHeader)
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
// It returns an error (never panics) if called on a request that did not
// pass through Middleware — a defensive check against future routing
// mistakes that bypass the middleware chain.
func RecordFromContext(ctx context.Context) (authkeys.Record, error) {
	record, ok := ctx.Value(contextKey{}).(authkeys.Record)
	if !ok {
		return authkeys.Record{}, amerrors.New(amerrors.CodeInternal, "authz: no authenticated record in context (middleware not applied?)")
	}
	return record, nil
}

// ProjectIDFromRequest adapts RecordFromContext to the function shape
// rest.NewTracesHandler expects, keeping the REST package free of any
// direct dependency on this package's context-key implementation detail.
func ProjectIDFromRequest(r *http.Request) (ids.ProjectID, error) {
	record, err := RecordFromContext(r.Context())
	if err != nil {
		return ids.ProjectID{}, err
	}
	return record.ProjectID, nil
}

func writeUnauthenticated(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte(`{"error":{"code":"unauthenticated","message":"missing or invalid API key"}}`)) //nolint:errcheck
}
