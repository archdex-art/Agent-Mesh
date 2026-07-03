//go:build integration

// Integration tests for AuthHandler and authz.SessionMiddleware against a
// real Postgres instance with schema/postgres/006_users.sql applied. Run
// with:
//
//	go test -tags integration ./internal/rest/... -run TestAuth -v
//
// Requires Postgres reachable at AGENTMESH_TEST_POSTGRES_DSN (same
// convention mcp_registry_test.go established; testMCPPool defaults to
// the docker-compose profile's host-mapped port and is reused here
// rather than duplicating pool setup). AuthHandler talks to *pgxpool.Pool
// directly rather than through a reader interface (auth.go's doc comment
// explains why, mirroring mcp_registry.go's own rationale), so — same as
// that suite — there is no seam to fake at unit-test scope; register/
// login/session round trips and cross-cutting auth rejections are
// verified against real Postgres constraint and query behavior.
package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentmesh/agentmesh/services/query-api/internal/authz"
	"github.com/agentmesh/agentmesh/shared/authkeys"
)

// authedRequest builds a request carrying the Authorization: Bearer
// header authz.SessionMiddleware expects — postJSON (mcp_registry_test.go)
// covers the unauthenticated POST case but has no way to attach headers
// or issue GETs, hence this sibling helper.
func authedRequest(t *testing.T, method, path, token string, body any) *http.Request {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshaling request body: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

func doRequest(handler http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// registerAndLogin is the round trip every other test in this file
// starts from: a freshly registered account, logged in for a live
// session token. Each call uses a random-suffixed email (mcpRandomSuffix,
// mcp_registry_test.go) so concurrent test runs against the same live
// Postgres instance never collide on users.email's UNIQUE constraint.
func registerAndLogin(t *testing.T, publicHandler http.Handler) (userID, email, password, sessionToken string) {
	t.Helper()
	email = "auth-test-" + mcpRandomSuffix(t) + "@example.com"
	password = "correct-horse-battery-staple"

	regRec := postJSON(t, publicHandler, "/v1/auth/register", registerRequest{Email: email, Password: password})
	if regRec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want %d, body=%s", regRec.Code, http.StatusCreated, regRec.Body.String())
	}
	var regResp struct {
		UserID string `json:"user_id"`
	}
	decodeJSON(t, regRec, &regResp)
	if regResp.UserID == "" {
		t.Fatalf("register response missing user_id: %s", regRec.Body.String())
	}
	userID = regResp.UserID

	loginRec := postJSON(t, publicHandler, "/v1/auth/login", loginRequest{Email: email, Password: password})
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d, body=%s", loginRec.Code, http.StatusOK, loginRec.Body.String())
	}
	var loginResp struct {
		SessionToken string `json:"session_token"`
		UserID       string `json:"user_id"`
	}
	decodeJSON(t, loginRec, &loginResp)
	if loginResp.UserID != userID {
		t.Fatalf("login user_id = %q, want %q", loginResp.UserID, userID)
	}
	if loginResp.SessionToken == "" {
		t.Fatalf("login response missing session_token: %s", loginRec.Body.String())
	}
	sessionToken = loginResp.SessionToken
	return userID, email, password, sessionToken
}

func TestAuthRegisterAndLoginRoundTrip(t *testing.T) {
	pool := testMCPPool(t)
	publicHandler := NewAuthHandler(pool)
	sessionHandler := authz.SessionMiddleware(pool)(publicHandler)

	userID, email, _, token := registerAndLogin(t, publicHandler)
	if !strings.HasPrefix(token, "ams_") {
		t.Fatalf("session token = %q, want ams_ prefix", token)
	}

	meRec := doRequest(sessionHandler, authedRequest(t, http.MethodGet, "/v1/auth/me", token, nil))
	if meRec.Code != http.StatusOK {
		t.Fatalf("me status = %d, want %d, body=%s", meRec.Code, http.StatusOK, meRec.Body.String())
	}
	var meResp struct {
		UserID string `json:"user_id"`
		Email  string `json:"email"`
	}
	decodeJSON(t, meRec, &meResp)
	if meResp.UserID != userID || meResp.Email != email {
		t.Fatalf("me response = %+v, want user_id=%q email=%q", meResp, userID, email)
	}

	// The session token's hash, not the raw token, must be what is
	// actually persisted — asserting this directly against Postgres is
	// the same style as mcp_registry_test.go's token-issuance hash test.
	var storedHash string
	if err := pool.QueryRow(context.Background(), `SELECT token_hash FROM sessions WHERE user_id = $1`, userID).Scan(&storedHash); err != nil {
		t.Fatalf("querying stored session: %v", err)
	}
	if storedHash != authkeys.Hash(token) {
		t.Fatalf("stored session token_hash does not match authkeys.Hash(raw session token)")
	}
}

func TestAuthLoginRejectsWrongPasswordAndUnknownEmail(t *testing.T) {
	pool := testMCPPool(t)
	handler := NewAuthHandler(pool)
	email := "auth-test-" + mcpRandomSuffix(t) + "@example.com"

	regRec := postJSON(t, handler, "/v1/auth/register", registerRequest{Email: email, Password: "correct-password"})
	if regRec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want %d, body=%s", regRec.Code, http.StatusCreated, regRec.Body.String())
	}

	wrongPasswordRec := postJSON(t, handler, "/v1/auth/login", loginRequest{Email: email, Password: "wrong-password"})
	if wrongPasswordRec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-password login status = %d, want %d, body=%s", wrongPasswordRec.Code, http.StatusUnauthorized, wrongPasswordRec.Body.String())
	}

	unknownEmailRec := postJSON(t, handler, "/v1/auth/login", loginRequest{Email: "nobody-" + mcpRandomSuffix(t) + "@example.com", Password: "whatever123"})
	if unknownEmailRec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown-email login status = %d, want %d, body=%s", unknownEmailRec.Code, http.StatusUnauthorized, unknownEmailRec.Body.String())
	}

	// Both failure modes must be indistinguishable to the caller (this
	// file's contract: "never reveal which of the two was wrong").
	var wrongBody, unknownBody struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeJSON(t, wrongPasswordRec, &wrongBody)
	decodeJSON(t, unknownEmailRec, &unknownBody)
	if wrongBody.Error.Message != unknownBody.Error.Message || wrongBody.Error.Code != unknownBody.Error.Code {
		t.Fatalf("wrong-password and unknown-email responses differ: %+v vs %+v", wrongBody, unknownBody)
	}
}

func TestAuthRegisterDuplicateEmailReturns409(t *testing.T) {
	pool := testMCPPool(t)
	handler := NewAuthHandler(pool)
	email := "auth-test-" + mcpRandomSuffix(t) + "@example.com"

	firstRec := postJSON(t, handler, "/v1/auth/register", registerRequest{Email: email, Password: "first-password"})
	if firstRec.Code != http.StatusCreated {
		t.Fatalf("first register status = %d, want %d, body=%s", firstRec.Code, http.StatusCreated, firstRec.Body.String())
	}

	dupRec := postJSON(t, handler, "/v1/auth/register", registerRequest{Email: email, Password: "second-password"})
	if dupRec.Code != http.StatusConflict {
		t.Fatalf("duplicate register status = %d, want %d, body=%s", dupRec.Code, http.StatusConflict, dupRec.Body.String())
	}
}

func TestAuthRegisterValidationRejectsBadEmailAndShortPassword(t *testing.T) {
	// Deliberately backed by a nil pool: register validates email shape
	// and password length before ever calling h.pool.QueryRow, so a
	// request that reached Postgres here would panic on the nil pointer
	// — this proves validation happens first, not merely that it
	// happens (mirrors TestMCPServerTransportValidationRejectsBogusValue).
	handler := NewAuthHandler(nil)

	badEmailRec := postJSON(t, handler, "/v1/auth/register", registerRequest{Email: "not-an-email", Password: "longenoughpassword"})
	if badEmailRec.Code != http.StatusBadRequest {
		t.Fatalf("bad email status = %d, want %d, body=%s", badEmailRec.Code, http.StatusBadRequest, badEmailRec.Body.String())
	}

	shortPasswordRec := postJSON(t, handler, "/v1/auth/register", registerRequest{Email: "valid@example.com", Password: "short"})
	if shortPasswordRec.Code != http.StatusBadRequest {
		t.Fatalf("short password status = %d, want %d, body=%s", shortPasswordRec.Code, http.StatusBadRequest, shortPasswordRec.Body.String())
	}
}

func TestAuthMeAndProjectsRejectMissingExpiredAndRevokedSession(t *testing.T) {
	pool := testMCPPool(t)
	publicHandler := NewAuthHandler(pool)
	sessionHandler := authz.SessionMiddleware(pool)(publicHandler)

	noTokenRec := doRequest(sessionHandler, httptest.NewRequest(http.MethodGet, "/v1/auth/me", nil))
	if noTokenRec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want %d", noTokenRec.Code, http.StatusUnauthorized)
	}

	bogusRec := doRequest(sessionHandler, authedRequest(t, http.MethodGet, "/v1/auth/me", "ams_"+mcpRandomSuffix(t), nil))
	if bogusRec.Code != http.StatusUnauthorized {
		t.Fatalf("never-issued token status = %d, want %d", bogusRec.Code, http.StatusUnauthorized)
	}

	_, _, _, expiredToken := registerAndLogin(t, publicHandler)
	if _, err := pool.Exec(context.Background(),
		`UPDATE sessions SET expires_at = now() - interval '1 hour' WHERE token_hash = $1`,
		authkeys.Hash(expiredToken),
	); err != nil {
		t.Fatalf("expiring session: %v", err)
	}
	expiredRec := doRequest(sessionHandler, authedRequest(t, http.MethodGet, "/v1/auth/projects", expiredToken, nil))
	if expiredRec.Code != http.StatusUnauthorized {
		t.Fatalf("expired session status = %d, want %d", expiredRec.Code, http.StatusUnauthorized)
	}

	_, _, _, revokedToken := registerAndLogin(t, publicHandler)
	if _, err := pool.Exec(context.Background(),
		`UPDATE sessions SET revoked_at = now() WHERE token_hash = $1`,
		authkeys.Hash(revokedToken),
	); err != nil {
		t.Fatalf("revoking session: %v", err)
	}
	revokedRec := doRequest(sessionHandler, authedRequest(t, http.MethodGet, "/v1/auth/projects", revokedToken, nil))
	if revokedRec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session status = %d, want %d", revokedRec.Code, http.StatusUnauthorized)
	}
}

func TestAuthCreateProjectOwnedByCallerWithWorkingAPIKey(t *testing.T) {
	pool := testMCPPool(t)
	publicHandler := NewAuthHandler(pool)
	sessionHandler := authz.SessionMiddleware(pool)(publicHandler)

	userID, _, _, token := registerAndLogin(t, publicHandler)

	projectName := "my-first-project-" + mcpRandomSuffix(t)
	createRec := doRequest(sessionHandler, authedRequest(t, http.MethodPost, "/v1/auth/projects", token, createUserProjectRequest{Name: projectName}))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create project status = %d, want %d, body=%s", createRec.Code, http.StatusCreated, createRec.Body.String())
	}
	var createResp struct {
		ProjectID string `json:"project_id"`
		Name      string `json:"name"`
		APIKey    string `json:"api_key"`
	}
	decodeJSON(t, createRec, &createResp)
	if createResp.ProjectID == "" || createResp.APIKey == "" {
		t.Fatalf("unexpected create response: %+v", createResp)
	}
	if createResp.Name != projectName {
		t.Fatalf("create response name = %q, want %q", createResp.Name, projectName)
	}
	if !strings.HasPrefix(createResp.APIKey, "am_live_") {
		t.Fatalf("api key = %q, want am_live_ prefix", createResp.APIKey)
	}

	// The returned raw key's hash must match what is actually stored —
	// same style as TestMCPServerTokenIssuanceHashMatchesStored.
	var ownerUserID, hashedKey, storedPrefix string
	err := pool.QueryRow(context.Background(), `
		SELECT p.owner_user_id, ak.hashed_key, ak.prefix
		FROM projects p
		JOIN api_keys ak ON ak.project_id = p.id
		WHERE p.id = $1
	`, createResp.ProjectID).Scan(&ownerUserID, &hashedKey, &storedPrefix)
	if err != nil {
		t.Fatalf("querying created project/key: %v", err)
	}
	if ownerUserID != userID {
		t.Fatalf("owner_user_id = %q, want %q", ownerUserID, userID)
	}
	if hashedKey != authkeys.Hash(createResp.APIKey) {
		t.Fatalf("stored hashed_key does not match authkeys.Hash(returned api_key)")
	}
	expectedPrefix, err := authkeys.Prefix(createResp.APIKey)
	if err != nil {
		t.Fatalf("computing expected prefix: %v", err)
	}
	if storedPrefix != expectedPrefix {
		t.Fatalf("stored prefix = %q, want %q", storedPrefix, expectedPrefix)
	}

	listRec := doRequest(sessionHandler, authedRequest(t, http.MethodGet, "/v1/auth/projects", token, nil))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list projects status = %d, want %d, body=%s", listRec.Code, http.StatusOK, listRec.Body.String())
	}
	var listed []userProjectView
	decodeJSON(t, listRec, &listed)
	found := false
	for _, p := range listed {
		if p.ID == createResp.ProjectID {
			found = true
			if p.APIKeyPrefix == nil || *p.APIKeyPrefix != expectedPrefix {
				t.Fatalf("listed project api_key_prefix = %v, want %q", p.APIKeyPrefix, expectedPrefix)
			}
		}
	}
	if !found {
		t.Fatalf("created project %s not present in list response %+v", createResp.ProjectID, listed)
	}
}

func TestAuthCreateProjectDefaultsNameWhenOmitted(t *testing.T) {
	pool := testMCPPool(t)
	publicHandler := NewAuthHandler(pool)
	sessionHandler := authz.SessionMiddleware(pool)(publicHandler)

	_, _, _, token := registerAndLogin(t, publicHandler)

	// No body at all (mirrors a bare POST with no JSON payload) —
	// createProject must fall back to createProjectAndKey's default
	// name, not reject the request as a decode error.
	createRec := doRequest(sessionHandler, authedRequest(t, http.MethodPost, "/v1/auth/projects", token, nil))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create project with no body status = %d, want %d, body=%s", createRec.Code, http.StatusCreated, createRec.Body.String())
	}
	var createResp struct {
		ProjectID string `json:"project_id"`
		Name      string `json:"name"`
		APIKey    string `json:"api_key"`
	}
	decodeJSON(t, createRec, &createResp)
	if !strings.HasPrefix(createResp.Name, "Project ") {
		t.Fatalf("default project name = %q, want \"Project \" prefix", createResp.Name)
	}
}
