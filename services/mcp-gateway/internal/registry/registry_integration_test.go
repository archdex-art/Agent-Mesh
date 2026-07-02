//go:build integration

// Run with: go test -tags integration ./internal/registry/... -v
// Requires Postgres reachable at AGENTMESH_TEST_POSTGRES_DSN with
// schema/postgres/001_projects_and_api_keys.sql and
// schema/postgres/004_mcp_registry.sql already applied — mirroring the
// rigor established by shared/authkeys/postgres_store_integration_test.go
// for the sibling api_keys-backed store.
package registry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
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

// seedProject inserts a fresh project row, returning its ProjectID.
func seedProject(t *testing.T, pool *pgxpool.Pool) ids.ProjectID {
	t.Helper()
	ctx := context.Background()

	var projectIDStr string
	err := pool.QueryRow(ctx,
		`INSERT INTO projects (id, name) VALUES (gen_random_uuid(), $1) RETURNING id`,
		"registry-test-project-"+randomSuffix(t),
	).Scan(&projectIDStr)
	if err != nil {
		t.Fatalf("inserting project: %v", err)
	}
	projectID, err := ids.ParseProjectID(projectIDStr)
	if err != nil {
		t.Fatalf("parsing seeded project id: %v", err)
	}
	return projectID
}

// seedServer inserts a registered MCP server row for projectID.
func seedServer(t *testing.T, pool *pgxpool.Pool, projectID ids.ProjectID, name string) string {
	t.Helper()
	var serverIDStr string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO mcp_servers (id, project_id, name, upstream_url, transport, version, owner, manifest_yaml)
		 VALUES (gen_random_uuid(), $1, $2, 'http://upstream.internal:9000', 'streamable-http', '1.0.0', 'test-owner', 'name: '||$2)
		 RETURNING id`,
		projectID.String(), name,
	).Scan(&serverIDStr)
	if err != nil {
		t.Fatalf("inserting mcp_servers row: %v", err)
	}
	return serverIDStr
}

func TestPostgresStoreResolveServerSucceeds(t *testing.T) {
	pool := testPool(t)
	projectID := seedProject(t, pool)
	seedServer(t, pool, projectID, "weather-tool")

	store := NewPostgresStore(pool)
	server, err := store.ResolveServer(context.Background(), projectID, "weather-tool")
	if err != nil {
		t.Fatalf("ResolveServer: %v", err)
	}
	if server.Name != "weather-tool" || server.UpstreamURL != "http://upstream.internal:9000" || server.Transport != "streamable-http" {
		t.Fatalf("unexpected server: %+v", server)
	}
}

func TestPostgresStoreResolveServerMissesUnknownName(t *testing.T) {
	pool := testPool(t)
	projectID := seedProject(t, pool)

	store := NewPostgresStore(pool)
	_, err := store.ResolveServer(context.Background(), projectID, "does-not-exist")
	if amerrors.CodeOf(err) != amerrors.CodeNotFound {
		t.Fatalf("CodeOf(err) = %v, want %v", amerrors.CodeOf(err), amerrors.CodeNotFound)
	}
}

func TestPostgresStoreResolveServerMissesCrossTenantName(t *testing.T) {
	pool := testPool(t)
	ownerProject := seedProject(t, pool)
	otherProject := seedProject(t, pool)
	seedServer(t, pool, ownerProject, "shared-name")

	store := NewPostgresStore(pool)
	_, err := store.ResolveServer(context.Background(), otherProject, "shared-name")
	if amerrors.CodeOf(err) != amerrors.CodeNotFound {
		t.Fatalf("a server registered under another project must resolve as not-found, got %v", err)
	}
}

func TestPostgresStoreGuardrailPolicyReturnsEnabledRow(t *testing.T) {
	pool := testPool(t)
	projectID := seedProject(t, pool)
	serverIDStr := seedServer(t, pool, projectID, "sql-tool")

	const dsl = "policies:\n  - name: deny_all\n    target_tools: [\"x\"]\n    action: deny\n"
	_, err := pool.Exec(context.Background(),
		`INSERT INTO guardrail_policies (id, project_id, mcp_server_id, rule_dsl, enabled) VALUES (gen_random_uuid(), $1, $2, $3, true)`,
		projectID.String(), serverIDStr, dsl,
	)
	if err != nil {
		t.Fatalf("inserting guardrail_policies row: %v", err)
	}

	store := NewPostgresStore(pool)
	server, err := store.ResolveServer(context.Background(), projectID, "sql-tool")
	if err != nil {
		t.Fatalf("ResolveServer: %v", err)
	}

	ruleDSL, ok, err := store.GuardrailPolicy(context.Background(), server.ID)
	if err != nil {
		t.Fatalf("GuardrailPolicy: %v", err)
	}
	if !ok {
		t.Fatal("GuardrailPolicy() ok = false, want true for a server with an enabled policy row")
	}
	if ruleDSL != dsl {
		t.Fatalf("ruleDSL = %q, want %q", ruleDSL, dsl)
	}
}

func TestPostgresStoreGuardrailPolicyMissesWhenNoneEnabled(t *testing.T) {
	pool := testPool(t)
	projectID := seedProject(t, pool)
	serverIDStr := seedServer(t, pool, projectID, "no-policy-tool")

	store := NewPostgresStore(pool)
	server, err := store.ResolveServer(context.Background(), projectID, "no-policy-tool")
	if err != nil {
		t.Fatalf("ResolveServer: %v", err)
	}
	_ = serverIDStr

	_, ok, err := store.GuardrailPolicy(context.Background(), server.ID)
	if err != nil {
		t.Fatalf("GuardrailPolicy: %v", err)
	}
	if ok {
		t.Fatal("GuardrailPolicy() ok = true, want false for a server with no enabled policy row")
	}
}
