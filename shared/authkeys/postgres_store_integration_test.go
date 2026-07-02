//go:build integration

// Run with: go test -tags integration ./authkeys/... -v
// Requires Postgres reachable at AGENTMESH_TEST_POSTGRES_DSN with
// schema/postgres/001_projects_and_api_keys.sql already applied.
package authkeys

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

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

// seedProjectAndKey inserts a project and an API key row, returning the
// raw (unhashed) key string for the test to authenticate with.
func seedProjectAndKey(t *testing.T, pool *pgxpool.Pool, role Role) (rawKey string) {
	t.Helper()
	ctx := context.Background()

	var projectID string
	err := pool.QueryRow(ctx,
		`INSERT INTO projects (id, name) VALUES (gen_random_uuid(), $1) RETURNING id`,
		"test-project-"+randomSuffix(t),
	).Scan(&projectID)
	if err != nil {
		t.Fatalf("inserting project: %v", err)
	}

	rawKey = "am_live_" + randomSuffix(t) + randomSuffix(t)
	prefix, err := Prefix(rawKey)
	if err != nil {
		t.Fatalf("Prefix: %v", err)
	}

	_, err = pool.Exec(ctx,
		`INSERT INTO api_keys (id, project_id, hashed_key, prefix, role) VALUES (gen_random_uuid(), $1, $2, $3, $4)`,
		projectID, Hash(rawKey), prefix, string(role),
	)
	if err != nil {
		t.Fatalf("inserting api_key: %v", err)
	}
	return rawKey
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	// crypto/rand, not t.Name(): several tests in this file share a common
	// name prefix ("TestPostgresStoreLookupByHash..."), so a name-derived
	// suffix truncated to a fixed length collided across subtests and
	// tripped the projects.name UNIQUE constraint — caught by actually
	// running this suite against real Postgres, not by inspection.
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("generating random suffix: %v", err)
	}
	return hex.EncodeToString(buf)
}

func TestPostgresStoreLookupByHashSucceeds(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	rawKey := seedProjectAndKey(t, pool, RoleIngest)

	rec, err := store.LookupByHash(context.Background(), Hash(rawKey))
	if err != nil {
		t.Fatalf("LookupByHash: %v", err)
	}
	if rec.Role != RoleIngest {
		t.Fatalf("Role = %v, want %v", rec.Role, RoleIngest)
	}
}

func TestPostgresStoreLookupByHashMissesUnknownKey(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)

	_, err := store.LookupByHash(context.Background(), Hash("am_live_neverregistered"))
	if err == nil {
		t.Fatal("LookupByHash succeeded for an unregistered key, want error")
	}
}

func TestPostgresStoreLookupByHashMissesRevokedKey(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	rawKey := seedProjectAndKey(t, pool, RoleIngest)

	_, err := pool.Exec(context.Background(),
		`UPDATE api_keys SET revoked_at = now() WHERE hashed_key = $1`, Hash(rawKey))
	if err != nil {
		t.Fatalf("revoking key: %v", err)
	}

	_, err = store.LookupByHash(context.Background(), Hash(rawKey))
	if err == nil {
		t.Fatal("LookupByHash succeeded for a revoked key, want error")
	}
}

func TestAuthenticateEndToEndAgainstRealPostgres(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	rawKey := seedProjectAndKey(t, pool, RoleAdmin)

	rec, err := Authenticate(context.Background(), store, rawKey)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if rec.Role != RoleAdmin {
		t.Fatalf("Role = %v, want %v", rec.Role, RoleAdmin)
	}
}
