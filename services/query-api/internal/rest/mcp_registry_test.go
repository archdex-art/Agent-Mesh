//go:build integration

// Integration tests for MCPRegistryHandler against a real Postgres
// instance with schema/postgres/004_mcp_registry.sql applied. Run with:
//
//	go test -tags integration ./internal/rest/... -v
//
// Requires Postgres reachable at AGENTMESH_TEST_POSTGRES_DSN (defaults to
// the docker-compose profile's host-mapped port). MCPRegistryHandler talks
// to *pgxpool.Pool directly rather than through a reader interface (see
// mcp_registry.go's doc comment on why that mirrors SetupHandler instead
// of TracesHandler), so there is no seam to fake at unit-test scope —
// per Technical Roadmap.md §7 ("avoids mocking the database layer for
// anything beyond pure unit logic"), its create/list/get/delete/token
// round trip and cross-tenant scoping are verified against real Postgres
// transaction/constraint behavior. This mirrors
// shared/authkeys/postgres_store_integration_test.go's established
// pattern of a build-tagged, opt-in suite that is not part of the default
// `go test ./...` (which requires no live infrastructure).
// TestMCPServerTransportValidationRejectsBogusValue is the one case that
// never reaches Postgres, kept here anyway so all mcp_registry.go
// coverage lives in one file.
package rest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/agentmesh/agentmesh/shared/authkeys"
	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testMCPPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("AGENTMESH_TEST_POSTGRES_DSN")
	if dsn == "" {
		// Matches this service's own cmd/main.go default DSN, which in
		// turn matches deploy/docker-compose.yml's host-mapped Postgres
		// port — the same instance a developer already has running via
		// `docker compose up` needs no extra setup to run this suite.
		dsn = "postgres://agentmesh:agentmesh@localhost:15432/agentmesh"
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

func mcpRandomSuffix(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("generating random suffix: %v", err)
	}
	return hex.EncodeToString(buf)
}

// seedProject inserts a project row and returns its ProjectID, satisfying
// mcp_servers.project_id's FK constraint. mustProjectID/fixedProjectIDFunc
// below are reused from traces_test.go (same package, always compiled)
// rather than redefined here.
func seedProject(t *testing.T, pool *pgxpool.Pool) ids.ProjectID {
	t.Helper()
	projectID := mustProjectID(t)
	_, err := pool.Exec(context.Background(),
		`INSERT INTO projects (id, name) VALUES ($1, $2)`,
		projectID.String(), "mcp-registry-test-"+mcpRandomSuffix(t),
	)
	if err != nil {
		t.Fatalf("seeding project: %v", err)
	}
	return projectID
}

func newTestServerRequest(name string) createServerRequest {
	return createServerRequest{
		Name:         name,
		UpstreamURL:  "https://example.com/mcp",
		Transport:    "streamable-http",
		Version:      "1.0.0",
		Owner:        "test-owner",
		ManifestYAML: "name: " + name + "\n",
	}
}

func postJSON(t *testing.T, handler http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshaling request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("unmarshaling response body %q: %v", rec.Body.String(), err)
	}
}

func TestMCPServerCreateListGetDeleteRoundTrip(t *testing.T) {
	pool := testMCPPool(t)
	projectID := seedProject(t, pool)
	handler := NewMCPRegistryHandler(pool, fixedProjectIDFunc(projectID))

	createRec := postJSON(t, handler, "/v1/mcp/servers", newTestServerRequest("round-trip-server"))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d, body=%s", createRec.Code, http.StatusCreated, createRec.Body.String())
	}
	var created MCPServerView
	decodeJSON(t, createRec, &created)
	if created.ID == "" || created.Name != "round-trip-server" || created.Transport != "streamable-http" {
		t.Fatalf("unexpected created server: %+v", created)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/v1/mcp/servers", nil)
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d", listRec.Code, http.StatusOK)
	}
	var listed []MCPServerView
	decodeJSON(t, listRec, &listed)
	found := false
	for _, s := range listed {
		if s.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("created server %s not present in list response %+v", created.ID, listed)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/mcp/servers/"+created.ID, nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d, body=%s", getRec.Code, http.StatusOK, getRec.Body.String())
	}
	var fetched MCPServerView
	decodeJSON(t, getRec, &fetched)
	if fetched.ID != created.ID {
		t.Fatalf("fetched.ID = %q, want %q", fetched.ID, created.ID)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/v1/mcp/servers/"+created.ID, nil)
	delRec := httptest.NewRecorder()
	handler.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d", delRec.Code, http.StatusNoContent)
	}

	getAfterDeleteReq := httptest.NewRequest(http.MethodGet, "/v1/mcp/servers/"+created.ID, nil)
	getAfterDeleteRec := httptest.NewRecorder()
	handler.ServeHTTP(getAfterDeleteRec, getAfterDeleteReq)
	if getAfterDeleteRec.Code != http.StatusNotFound {
		t.Fatalf("get-after-delete status = %d, want %d", getAfterDeleteRec.Code, http.StatusNotFound)
	}
}

func TestMCPServerCreateWithRuleDSLInsertsEnabledGuardrailPolicy(t *testing.T) {
	pool := testMCPPool(t)
	projectID := seedProject(t, pool)
	handler := NewMCPRegistryHandler(pool, fixedProjectIDFunc(projectID))

	req := newTestServerRequest("policy-server")
	req.RuleDSL = "policies:\n  - name: deny_all\n    target_tools: [\"execute_query\"]\n    action: deny\n    rules: []\n"

	rec := postJSON(t, handler, "/v1/mcp/servers", req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d, body=%s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var created MCPServerView
	decodeJSON(t, rec, &created)

	var enabled bool
	var ruleDSL string
	err := pool.QueryRow(context.Background(),
		`SELECT enabled, rule_dsl FROM guardrail_policies WHERE mcp_server_id = $1`,
		created.ID,
	).Scan(&enabled, &ruleDSL)
	if err != nil {
		t.Fatalf("querying guardrail_policies: %v", err)
	}
	if !enabled {
		t.Fatalf("guardrail policy enabled = %v, want true", enabled)
	}
	if ruleDSL != req.RuleDSL {
		t.Fatalf("guardrail policy rule_dsl = %q, want %q", ruleDSL, req.RuleDSL)
	}
}

func TestMCPServerDuplicateNameReturns409(t *testing.T) {
	pool := testMCPPool(t)
	projectID := seedProject(t, pool)
	handler := NewMCPRegistryHandler(pool, fixedProjectIDFunc(projectID))

	first := postJSON(t, handler, "/v1/mcp/servers", newTestServerRequest("dup-server"))
	if first.Code != http.StatusCreated {
		t.Fatalf("first create status = %d, want %d, body=%s", first.Code, http.StatusCreated, first.Body.String())
	}

	second := postJSON(t, handler, "/v1/mcp/servers", newTestServerRequest("dup-server"))
	if second.Code != http.StatusConflict {
		t.Fatalf("second create status = %d, want %d, body=%s", second.Code, http.StatusConflict, second.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeJSON(t, second, &body)
	if body.Error.Code != "already_exists" {
		t.Fatalf("error.code = %q, want %q", body.Error.Code, "already_exists")
	}
}

func TestMCPServerCrossProjectGetAndDeleteReturn404(t *testing.T) {
	pool := testMCPPool(t)
	ownerProjectID := seedProject(t, pool)
	otherProjectID := seedProject(t, pool)

	ownerHandler := NewMCPRegistryHandler(pool, fixedProjectIDFunc(ownerProjectID))
	otherHandler := NewMCPRegistryHandler(pool, fixedProjectIDFunc(otherProjectID))

	createRec := postJSON(t, ownerHandler, "/v1/mcp/servers", newTestServerRequest("cross-tenant-server"))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d, body=%s", createRec.Code, http.StatusCreated, createRec.Body.String())
	}
	var created MCPServerView
	decodeJSON(t, createRec, &created)

	getReq := httptest.NewRequest(http.MethodGet, "/v1/mcp/servers/"+created.ID, nil)
	getRec := httptest.NewRecorder()
	otherHandler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("cross-project get status = %d, want %d", getRec.Code, http.StatusNotFound)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/v1/mcp/servers/"+created.ID, nil)
	delRec := httptest.NewRecorder()
	otherHandler.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNotFound {
		t.Fatalf("cross-project delete status = %d, want %d", delRec.Code, http.StatusNotFound)
	}

	// The server must still exist for its real owner — a cross-project
	// delete attempt must not have silently removed it.
	ownerGetReq := httptest.NewRequest(http.MethodGet, "/v1/mcp/servers/"+created.ID, nil)
	ownerGetRec := httptest.NewRecorder()
	ownerHandler.ServeHTTP(ownerGetRec, ownerGetReq)
	if ownerGetRec.Code != http.StatusOK {
		t.Fatalf("owner get status after cross-project delete attempt = %d, want %d", ownerGetRec.Code, http.StatusOK)
	}
}

func TestMCPServerTokenIssuanceHashMatchesStored(t *testing.T) {
	pool := testMCPPool(t)
	projectID := seedProject(t, pool)
	handler := NewMCPRegistryHandler(pool, fixedProjectIDFunc(projectID))

	createRec := postJSON(t, handler, "/v1/mcp/servers", newTestServerRequest("token-server"))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d, body=%s", createRec.Code, http.StatusCreated, createRec.Body.String())
	}
	var created MCPServerView
	decodeJSON(t, createRec, &created)

	tokenRec := postJSON(t, handler, "/v1/mcp/servers/"+created.ID+"/tokens", createTokenRequest{CallerName: "test-caller"})
	if tokenRec.Code != http.StatusCreated {
		t.Fatalf("token create status = %d, want %d, body=%s", tokenRec.Code, http.StatusCreated, tokenRec.Body.String())
	}
	var tokenResp struct {
		Token  string `json:"token"`
		Prefix string `json:"prefix"`
	}
	decodeJSON(t, tokenRec, &tokenResp)
	if !strings.HasPrefix(tokenResp.Token, "mcp_") {
		t.Fatalf("token = %q, want mcp_ prefix", tokenResp.Token)
	}

	var storedHash, storedPrefix, callerName string
	err := pool.QueryRow(context.Background(),
		`SELECT hashed_token, prefix, caller_name FROM mcp_server_tokens WHERE mcp_server_id = $1`,
		created.ID,
	).Scan(&storedHash, &storedPrefix, &callerName)
	if err != nil {
		t.Fatalf("querying mcp_server_tokens: %v", err)
	}
	if storedHash != authkeys.Hash(tokenResp.Token) {
		t.Fatalf("stored hash does not match authkeys.Hash(raw token)")
	}
	if storedPrefix != tokenResp.Prefix {
		t.Fatalf("storedPrefix = %q, want %q", storedPrefix, tokenResp.Prefix)
	}
	if callerName != "test-caller" {
		t.Fatalf("callerName = %q, want %q", callerName, "test-caller")
	}
}

func TestMCPServerTokenIssuanceCrossProjectReturns404(t *testing.T) {
	pool := testMCPPool(t)
	ownerProjectID := seedProject(t, pool)
	otherProjectID := seedProject(t, pool)

	ownerHandler := NewMCPRegistryHandler(pool, fixedProjectIDFunc(ownerProjectID))
	otherHandler := NewMCPRegistryHandler(pool, fixedProjectIDFunc(otherProjectID))

	createRec := postJSON(t, ownerHandler, "/v1/mcp/servers", newTestServerRequest("token-cross-tenant-server"))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d, body=%s", createRec.Code, http.StatusCreated, createRec.Body.String())
	}
	var created MCPServerView
	decodeJSON(t, createRec, &created)

	tokenRec := postJSON(t, otherHandler, "/v1/mcp/servers/"+created.ID+"/tokens", createTokenRequest{CallerName: "intruder"})
	if tokenRec.Code != http.StatusNotFound {
		t.Fatalf("cross-project token create status = %d, want %d", tokenRec.Code, http.StatusNotFound)
	}
}

func TestMCPServerTransportValidationRejectsBogusValue(t *testing.T) {
	// Deliberately backed by a nil pool: mcp_registry.go's createServer
	// validates transport before ever calling h.pool.Begin, so a request
	// that reaches Postgres here would panic on the nil pointer — this
	// proves validation happens first, not merely that it happens.
	handler := NewMCPRegistryHandler(nil, fixedProjectIDFunc(mustProjectID(t)))

	req := newTestServerRequest("bad-transport-server")
	req.Transport = "carrier-pigeon"

	rec := postJSON(t, handler, "/v1/mcp/servers", req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeJSON(t, rec, &body)
	if body.Error.Code != "invalid_argument" {
		t.Fatalf("error.code = %q, want %q", body.Error.Code, "invalid_argument")
	}
}
