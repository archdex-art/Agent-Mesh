// session.go adds session-token authentication alongside this package's
// existing API-key Middleware/ProjectIDFromRequest — a NEW, ADDITIONAL
// auth layer used only by the new account-management endpoints
// (POST /v1/auth/register|login, GET /v1/auth/me, GET|POST
// /v1/auth/projects). It never replaces, and is never consulted by,
// Middleware/ProjectIDFromRequest: every existing project-data endpoint
// (traces.go, mcp_registry.go, internal/graphql/schema.go) keeps
// authenticating exclusively via the X-AgentMesh-API-Key header, exactly
// as before (schema/postgres/006_users.sql's migration header comment).
package authz

import (
	"context"
	"net/http"
	"strings"

	"github.com/agentmesh/agentmesh/shared/authkeys"
	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/jackc/pgx/v5/pgxpool"
)

// bearerPrefix is the scheme prefix for the Authorization header session
// tokens travel in — deliberately a different header AND scheme
// (Authorization: Bearer, not X-AgentMesh-API-Key) than API-key auth, so
// the two credentials can never be confused for one another in a
// request or accidentally accepted by the wrong middleware.
const bearerPrefix = "Bearer "

// sessionContextKey is a sibling of Middleware's contextKey — a distinct
// type in this same package (not a new package: session auth is a
// variant of this package's existing auth concern, not an unrelated
// one), so an API-key-authenticated request and a session-authenticated
// request can never collide in the same context even though both keys
// live here.
type sessionContextKey struct{}

// sessionPrincipal is what SessionMiddleware stashes in the request
// context: just enough identity for the account-management endpoints
// (who is calling), deliberately not sharing authkeys.Record's shape —
// a session belongs to a user, not a project, and has no Role/ProjectID.
type sessionPrincipal struct {
	userID string
	email  string
}

// SessionMiddleware authenticates every request via the
// `Authorization: Bearer <token>` header: hashes the raw token with
// authkeys.Hash — the same SHA-256 algorithm API keys use, reused rather
// than reimplemented, because a session token is exactly as
// high-entropy and machine-generated as an API key (schema/postgres/
// 006_users.sql's doc comment: only *passwords* need bcrypt's slow KDF,
// not this) — then looks it up joined to users, requiring the session
// be both unrevoked and unexpired.
func SessionMiddleware(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawToken, ok := strings.CutPrefix(r.Header.Get("Authorization"), bearerPrefix)
			if !ok || rawToken == "" {
				writeSessionUnauthenticated(w)
				return
			}

			tokenHash := authkeys.Hash(rawToken)

			var principal sessionPrincipal
			err := pool.QueryRow(r.Context(), `
				SELECT u.id, u.email
				FROM sessions s
				JOIN users u ON u.id = s.user_id
				WHERE s.token_hash = $1 AND s.revoked_at IS NULL AND s.expires_at > now()
			`, tokenHash).Scan(&principal.userID, &principal.email)
			if err != nil {
				// Every failure mode (no such session, revoked, expired,
				// or a transient lookup error) collapses to the same
				// generic 401 — mirrors Middleware's stance of never
				// distinguishing "invalid" from "unavailable" to the
				// caller.
				writeSessionUnauthenticated(w)
				return
			}

			ctx := context.WithValue(r.Context(), sessionContextKey{}, principal)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserFromContext retrieves the (user_id, email) stashed by
// SessionMiddleware. It returns an error (never panics) if called on a
// request that did not pass through SessionMiddleware — the same
// defensive stance as RecordFromContext.
func UserFromContext(ctx context.Context) (userID, email string, err error) {
	principal, ok := ctx.Value(sessionContextKey{}).(sessionPrincipal)
	if !ok {
		return "", "", amerrors.New(amerrors.CodeInternal, "authz: no authenticated session in context (SessionMiddleware not applied?)")
	}
	return principal.userID, principal.email, nil
}

func writeSessionUnauthenticated(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte(`{"error":{"code":"unauthenticated","message":"missing or invalid session token"}}`)) //nolint:errcheck
}
