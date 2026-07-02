//go:build integration

// Run with: go test -tags integration ./internal/oauth/... -v
// Requires Postgres reachable at AGENTMESH_TEST_POSTGRES_DSN with
// schema/postgres/001_projects_and_api_keys.sql and
// schema/postgres/004_mcp_registry.sql already applied — mirroring the
// rigor established by shared/authkeys/postgres_store_integration_test.go
// for the sibling api_keys-backed store.
package oauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/agentmesh/agentmesh/shared/authkeys"
	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("AGENTMESH_TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = "postgres://agentmesh:agentmesh@localhost:5432/agentmesh"
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connecting to postgres: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("pinging postgres: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("generating random suffix: %v", err)
	}
	return hex.EncodeToString(buf)
}

// seedServerForToken inserts a project + mcp_servers row, returning the
// new server's id (as a string, matching TokenRecord.MCPServerID's shape).
func seedServerForToken(t *testing.T, pool *pgxpool.Pool) (serverID string) {
	t.Helper()
	ctx := context.Background()

	var projectID string
	err := pool.QueryRow(ctx,
		`INSERT INTO projects (id, name) VALUES (gen_random_uuid(), $1) RETURNING id`,
		"oauth-test-project-"+randomSuffix(t),
	).Scan(&projectID)
	if err != nil {
		t.Fatalf("inserting project: %v", err)
	}

	err = pool.QueryRow(ctx,
		`INSERT INTO mcp_servers (id, project_id, name, upstream_url, transport, version, owner, manifest_yaml)
		 VALUES (gen_random_uuid(), $1, $2, 'http://upstream.internal:9000', 'streamable-http', '1.0.0', 'test-owner', 'name: '||$2)
		 RETURNING id`,
		projectID, "token-test-server-"+randomSuffix(t),
	).Scan(&serverID)
	if err != nil {
		t.Fatalf("inserting mcp_servers row: %v", err)
	}
	return serverID
}

// seedToken inserts an mcp_server_tokens row for serverID, returning the
// raw (unhashed) token for the test to authenticate with.
func seedToken(t *testing.T, pool *pgxpool.Pool, serverID, callerName string, revoked bool) (rawToken string) {
	t.Helper()
	rawToken = tokenPrefix + randomSuffix(t) + randomSuffix(t)
	revokedExpr := "NULL"
	if revoked {
		revokedExpr = "now()"
	}
	_, err := pool.Exec(context.Background(),
		`INSERT INTO mcp_server_tokens (id, mcp_server_id, hashed_token, prefix, caller_name, revoked_at)
		 VALUES (gen_random_uuid(), $1, $2, $3, $4, `+revokedExpr+`)`,
		serverID, authkeys.Hash(rawToken), rawToken[:12], callerName,
	)
	if err != nil {
		t.Fatalf("inserting mcp_server_tokens row: %v", err)
	}
	return rawToken
}

func TestPostgresStoreLookupByHashSucceeds(t *testing.T) {
	pool := testPool(t)
	serverID := seedServerForToken(t, pool)
	rawToken := seedToken(t, pool, serverID, "billing-agent", false)

	store := NewPostgresStore(pool)
	rec, err := store.LookupByHash(context.Background(), authkeys.Hash(rawToken))
	if err != nil {
		t.Fatalf("LookupByHash: %v", err)
	}
	if rec.MCPServerID != serverID {
		t.Fatalf("MCPServerID = %q, want %q", rec.MCPServerID, serverID)
	}
	if rec.CallerName != "billing-agent" {
		t.Fatalf("CallerName = %q, want %q", rec.CallerName, "billing-agent")
	}
}

func TestPostgresStoreLookupByHashMissesUnknownToken(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)

	_, err := store.LookupByHash(context.Background(), authkeys.Hash("mcp_neverexisted"))
	if amerrors.CodeOf(err) != amerrors.CodeNotFound {
		t.Fatalf("CodeOf(err) = %v, want %v", amerrors.CodeOf(err), amerrors.CodeNotFound)
	}
}

func TestPostgresStoreLookupByHashMissesRevokedToken(t *testing.T) {
	pool := testPool(t)
	serverID := seedServerForToken(t, pool)
	rawToken := seedToken(t, pool, serverID, "revoked-caller", true)

	store := NewPostgresStore(pool)
	_, err := store.LookupByHash(context.Background(), authkeys.Hash(rawToken))
	if amerrors.CodeOf(err) != amerrors.CodeNotFound {
		t.Fatalf("a revoked token must miss like an unknown one; CodeOf(err) = %v, want %v", amerrors.CodeOf(err), amerrors.CodeNotFound)
	}
}

func TestAuthenticateEndToEndAgainstRealPostgres(t *testing.T) {
	pool := testPool(t)
	serverID := seedServerForToken(t, pool)
	rawToken := seedToken(t, pool, serverID, "e2e-caller", false)

	store := NewPostgresStore(pool)
	rec, err := Authenticate(context.Background(), store, rawToken)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if rec.MCPServerID != serverID {
		t.Fatalf("MCPServerID = %q, want %q", rec.MCPServerID, serverID)
	}
}
