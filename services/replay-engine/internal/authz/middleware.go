// Package authz implements the Replay Engine's developer-facing HTTP
// authentication, identical in shape to the Query API's
// (services/query-api/internal/authz/middleware.go) since both are
// REST surfaces authenticated the same way (Architecture.md §13's single
// authkeys.Authenticate entry point). Duplicated rather than imported for
// the same reason internal/store duplicates the Query API's ClickHouse
// reader — see that package's doc comment.
//
// This middleware guards only /v1/replay (the developer/CLI-facing
// start/complete API); the SDK-facing lookup endpoint
// (internal/rest.LookupHandler) is intentionally left unauthenticated —
// see that handler's doc comment for why.
package authz

import (
	"context"
	"net/http"

	"github.com/agentmesh/agentmesh/shared/authkeys"
	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
)

const apiKeyHeader = "X-AgentMesh-API-Key"

type contextKey struct{}

// Middleware authenticates every request via apiKeyHeader and, on
// success, stashes the resolved authkeys.Record in the request context.
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
func RecordFromContext(ctx context.Context) (authkeys.Record, error) {
	record, ok := ctx.Value(contextKey{}).(authkeys.Record)
	if !ok {
		return authkeys.Record{}, amerrors.New(amerrors.CodeInternal, "authz: no authenticated record in context (middleware not applied?)")
	}
	return record, nil
}

// ProjectIDFromRequest adapts RecordFromContext to the function shape
// rest.NewReplayHandler expects.
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
