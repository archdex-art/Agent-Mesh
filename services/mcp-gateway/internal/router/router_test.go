package router

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentmesh/agentmesh/services/mcp-gateway/internal/audit"
	"github.com/agentmesh/agentmesh/services/mcp-gateway/internal/authz"
	"github.com/agentmesh/agentmesh/services/mcp-gateway/internal/oauth"
	"github.com/agentmesh/agentmesh/services/mcp-gateway/internal/policy"
	"github.com/agentmesh/agentmesh/services/mcp-gateway/internal/ratelimit"
	"github.com/agentmesh/agentmesh/services/mcp-gateway/internal/registry"
	"github.com/agentmesh/agentmesh/shared/authkeys"
	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

// --- fakes (no live Postgres/Redis required) -------------------------

// fakeRegistryStore implements registry.Store, mirroring the
// collector/internal/ingest fakeAuthStore-style pattern this repo uses
// throughout for "independently testable" package boundaries.
type fakeRegistryStore struct {
	server         registry.Server
	resolveErr     error
	ruleDSL        string
	hasPolicy      bool
	policyErr      error
	resolveCalls   int
	guardrailCalls int
}

func (f *fakeRegistryStore) ResolveServer(ctx context.Context, projectID ids.ProjectID, name string) (registry.Server, error) {
	f.resolveCalls++
	if f.resolveErr != nil {
		return registry.Server{}, f.resolveErr
	}
	return f.server, nil
}

func (f *fakeRegistryStore) GuardrailPolicy(ctx context.Context, serverID uuid.UUID) (string, bool, error) {
	f.guardrailCalls++
	if f.policyErr != nil {
		return "", false, f.policyErr
	}
	return f.ruleDSL, f.hasPolicy, nil
}

// fakeOAuthStore implements oauth.Store against an in-memory map keyed by
// hashed token, exactly like oauth_test.go's fakeStore.
type fakeOAuthStore struct {
	byHash map[string]oauth.TokenRecord
}

func (f *fakeOAuthStore) LookupByHash(ctx context.Context, hashedToken string) (oauth.TokenRecord, error) {
	rec, ok := f.byHash[hashedToken]
	if !ok {
		return oauth.TokenRecord{}, amerrors.New(amerrors.CodeNotFound, "not found")
	}
	return rec, nil
}

// fakeAuditEmitter implements proxy.AuditEmitter, mirroring
// proxy/audit_interceptor_test.go's fakeEmitter.
type fakeAuditEmitter struct {
	calls []audit.Call
}

func (f *fakeAuditEmitter) Emit(ctx context.Context, call audit.Call) error {
	f.calls = append(f.calls, call)
	return nil
}

// --- test helpers ------------------------------------------------------

func mustProjectID(t *testing.T) ids.ProjectID {
	t.Helper()
	id, err := ids.NewProjectID()
	if err != nil {
		t.Fatalf("NewProjectID: %v", err)
	}
	return id
}

func emptyFallbackEngine(t *testing.T) *policy.Engine {
	t.Helper()
	engine, err := policy.Load([]byte("policies: []"))
	if err != nil {
		t.Fatalf("policy.Load: %v", err)
	}
	return engine
}

// contextWithProjectID drives a real authz.Middleware pass to obtain a
// context carrying the authenticated ProjectID, mirroring
// proxy/audit_interceptor_test.go's contextWithProjectID exactly (authz's
// context key type is unexported by design, so this is the only correct
// way to construct such a context from outside that package).
func contextWithProjectID(t *testing.T, projectID ids.ProjectID) context.Context {
	t.Helper()
	store := &staticAuthStore{record: authkeys.Record{ProjectID: projectID, Role: authkeys.RoleIngest}}

	var captured context.Context
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Context()
	})
	handler := authz.Middleware(store)(inner)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set(authz.APIKeyHeader, "am_live_testkey1234567890")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if captured == nil {
		t.Fatal("authz.Middleware did not call through to the inner handler")
	}
	return captured
}

type staticAuthStore struct {
	record authkeys.Record
}

func (s *staticAuthStore) LookupByHash(ctx context.Context, hashedKey string) (authkeys.Record, error) {
	return s.record, nil
}

// newTestRedisLimiter returns a Limiter backed by a real in-memory
// miniredis server, exercising Allow's real INCR/EXPIRE logic (not a
// stubbed-out fake) inside router-level tests.
func newTestRedisLimiter(t *testing.T) *ratelimit.Limiter {
	t.Helper()
	mr := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { client.Close() })
	return ratelimit.New(client)
}

const rpcToolsCallBody = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"execute_query","arguments":{"sql":"DROP TABLE x"}}}`
const rpcPingBody = `{"jsonrpc":"2.0","id":1,"method":"ping"}`

func decodeRPCError(t *testing.T, body []byte) (code int, message string) {
	t.Helper()
	var resp struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decoding JSON-RPC response: %v (body: %s)", err, body)
	}
	if resp.Error == nil {
		t.Fatalf("response has no JSON-RPC error object (body: %s)", body)
	}
	return resp.Error.Code, resp.Error.Message
}

// --- ServeHTTP tests -----------------------------------------------------

func TestRouterForwardsAllowedCallToResolvedUpstreamAndAudits(t *testing.T) {
	upstreamHit := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit = true
		if r.URL.Path != "/tools/call" {
			t.Errorf("upstream received path %q, want /tools/call (server-name prefix should be stripped)", r.URL.Path)
		}
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"ok"}`))
	}))
	defer upstream.Close()

	projectID := mustProjectID(t)
	serverID := uuid.New()
	rawToken := "mcp_validtoken1234567890"

	reg := &fakeRegistryStore{server: registry.Server{ID: serverID, Name: "weather", UpstreamURL: upstream.URL}}
	oauthStore := &fakeOAuthStore{byHash: map[string]oauth.TokenRecord{
		authkeys.Hash(rawToken): {MCPServerID: serverID.String(), CallerName: "billing-agent"},
	}}
	emitter := &fakeAuditEmitter{}
	rt := New(reg, oauthStore, nil, emitter, emptyFallbackEngine(t))

	req := httptest.NewRequest(http.MethodPost, "/v1/mcp/weather/tools/call", bytes.NewBufferString(rpcToolsCallBody))
	req = req.WithContext(contextWithProjectID(t, projectID))
	req.Header.Set("Authorization", "Bearer "+rawToken)
	rec := httptest.NewRecorder()

	rt.ServeHTTP(rec, req)

	if !upstreamHit {
		t.Fatal("upstream was never hit")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(emitter.calls) != 1 {
		t.Fatalf("emitter received %d calls, want 1", len(emitter.calls))
	}
	if emitter.calls[0].CallerRecord != "billing-agent" {
		t.Fatalf("CallerRecord = %q, want %q (bearer token's caller_name)", emitter.calls[0].CallerRecord, "billing-agent")
	}
	if emitter.calls[0].ProjectID != projectID {
		t.Fatalf("ProjectID = %v, want %v", emitter.calls[0].ProjectID, projectID)
	}
}

func TestRouterForwardsBareServerPathToUpstreamRoot(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			t.Errorf("upstream received path %q, want / for a bare server-name request", r.URL.Path)
		}
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"ok"}`))
	}))
	defer upstream.Close()

	serverID := uuid.New()
	rawToken := "mcp_anothertoken"
	reg := &fakeRegistryStore{server: registry.Server{ID: serverID, Name: "weather", UpstreamURL: upstream.URL}}
	oauthStore := &fakeOAuthStore{byHash: map[string]oauth.TokenRecord{
		authkeys.Hash(rawToken): {MCPServerID: serverID.String(), CallerName: "caller"},
	}}
	rt := New(reg, oauthStore, nil, &fakeAuditEmitter{}, emptyFallbackEngine(t))

	req := httptest.NewRequest(http.MethodPost, "/v1/mcp/weather", bytes.NewBufferString(rpcPingBody))
	req = req.WithContext(contextWithProjectID(t, mustProjectID(t)))
	req.Header.Set("Authorization", "Bearer "+rawToken)
	rec := httptest.NewRecorder()

	rt.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRouterRejectsUnknownServerName(t *testing.T) {
	reg := &fakeRegistryStore{resolveErr: amerrors.New(amerrors.CodeNotFound, "no such server")}
	oauthStore := &fakeOAuthStore{byHash: map[string]oauth.TokenRecord{}}
	rt := New(reg, oauthStore, nil, &fakeAuditEmitter{}, emptyFallbackEngine(t))

	req := httptest.NewRequest(http.MethodPost, "/v1/mcp/ghost/tools/call", bytes.NewBufferString(rpcPingBody))
	req = req.WithContext(contextWithProjectID(t, mustProjectID(t)))
	req.Header.Set("Authorization", "Bearer mcp_whatever")
	rec := httptest.NewRecorder()

	rt.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (JSON-RPC errors stay HTTP 200)", rec.Code)
	}
	code, _ := decodeRPCError(t, rec.Body.Bytes())
	if code != codeServerNotFound {
		t.Fatalf("error code = %d, want %d", code, codeServerNotFound)
	}
}

func TestRouterRejectsRequestMissingBearerToken(t *testing.T) {
	upstreamHit := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit = true
	}))
	defer upstream.Close()

	serverID := uuid.New()
	reg := &fakeRegistryStore{server: registry.Server{ID: serverID, Name: "weather", UpstreamURL: upstream.URL}}
	oauthStore := &fakeOAuthStore{byHash: map[string]oauth.TokenRecord{}}
	rt := New(reg, oauthStore, nil, &fakeAuditEmitter{}, emptyFallbackEngine(t))

	req := httptest.NewRequest(http.MethodPost, "/v1/mcp/weather/tools/call", bytes.NewBufferString(rpcPingBody))
	req = req.WithContext(contextWithProjectID(t, mustProjectID(t)))
	// No Authorization header set at all.
	rec := httptest.NewRecorder()

	rt.ServeHTTP(rec, req)

	if upstreamHit {
		t.Fatal("upstream was hit despite a missing bearer token")
	}
	code, _ := decodeRPCError(t, rec.Body.Bytes())
	if code != codeUnauthenticated {
		t.Fatalf("error code = %d, want %d", code, codeUnauthenticated)
	}
}

func TestRouterRejectsTokenScopedToAnotherServer(t *testing.T) {
	upstreamHit := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit = true
	}))
	defer upstream.Close()

	serverID := uuid.New()
	otherServerID := uuid.New()
	rawToken := "mcp_tokenforanotherserver"

	reg := &fakeRegistryStore{server: registry.Server{ID: serverID, Name: "weather", UpstreamURL: upstream.URL}}
	oauthStore := &fakeOAuthStore{byHash: map[string]oauth.TokenRecord{
		authkeys.Hash(rawToken): {MCPServerID: otherServerID.String(), CallerName: "someone-else"},
	}}
	rt := New(reg, oauthStore, nil, &fakeAuditEmitter{}, emptyFallbackEngine(t))

	req := httptest.NewRequest(http.MethodPost, "/v1/mcp/weather/tools/call", bytes.NewBufferString(rpcPingBody))
	req = req.WithContext(contextWithProjectID(t, mustProjectID(t)))
	req.Header.Set("Authorization", "Bearer "+rawToken)
	rec := httptest.NewRecorder()

	rt.ServeHTTP(rec, req)

	if upstreamHit {
		t.Fatal("upstream was hit despite a token scoped to a different server")
	}
	code, _ := decodeRPCError(t, rec.Body.Bytes())
	if code != codeUnauthenticated {
		t.Fatalf("error code = %d, want %d", code, codeUnauthenticated)
	}
}

func TestRouterFailsClosedWithoutAuthenticatedProjectInContext(t *testing.T) {
	rt := New(&fakeRegistryStore{}, &fakeOAuthStore{}, nil, &fakeAuditEmitter{}, emptyFallbackEngine(t))

	// No authz.Middleware pass: plain background context, simulating a
	// wiring mistake where this Router was mounted unprotected.
	req := httptest.NewRequest(http.MethodPost, "/v1/mcp/weather/tools/call", bytes.NewBufferString(rpcPingBody))
	rec := httptest.NewRecorder()

	rt.ServeHTTP(rec, req)

	code, _ := decodeRPCError(t, rec.Body.Bytes())
	if code != codeUnauthenticated {
		t.Fatalf("error code = %d, want %d", code, codeUnauthenticated)
	}
}

func TestRouterRejectsMalformedPath(t *testing.T) {
	rt := New(&fakeRegistryStore{}, &fakeOAuthStore{}, nil, &fakeAuditEmitter{}, emptyFallbackEngine(t))

	req := httptest.NewRequest(http.MethodPost, "/v1/mcp/", bytes.NewBufferString(rpcPingBody))
	req = req.WithContext(contextWithProjectID(t, mustProjectID(t)))
	rec := httptest.NewRecorder()

	rt.ServeHTTP(rec, req)

	code, _ := decodeRPCError(t, rec.Body.Bytes())
	if code != codeInvalidRequest {
		t.Fatalf("error code = %d, want %d", code, codeInvalidRequest)
	}
}

func TestRouterRejectsNonPostMethod(t *testing.T) {
	rt := New(&fakeRegistryStore{}, &fakeOAuthStore{}, nil, &fakeAuditEmitter{}, emptyFallbackEngine(t))

	req := httptest.NewRequest(http.MethodGet, "/v1/mcp/weather", nil)
	req = req.WithContext(contextWithProjectID(t, mustProjectID(t)))
	rec := httptest.NewRecorder()

	rt.ServeHTTP(rec, req)

	code, _ := decodeRPCError(t, rec.Body.Bytes())
	if code != codeInvalidRequest {
		t.Fatalf("error code = %d, want %d", code, codeInvalidRequest)
	}
}

func TestRouterUsesPerServerGuardrailPolicyOverFallback(t *testing.T) {
	upstreamHit := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit = true
	}))
	defer upstream.Close()

	serverID := uuid.New()
	rawToken := "mcp_guardedtoken"
	const dsl = `
policies:
  - name: deny_drop
    target_tools: ["execute_query"]
    action: deny
    rules:
      - param_matches: {param: "sql", pattern: "(?i)DROP"}
`
	reg := &fakeRegistryStore{
		server:    registry.Server{ID: serverID, Name: "sql-tool", UpstreamURL: upstream.URL},
		ruleDSL:   dsl,
		hasPolicy: true,
	}
	oauthStore := &fakeOAuthStore{byHash: map[string]oauth.TokenRecord{
		authkeys.Hash(rawToken): {MCPServerID: serverID.String(), CallerName: "caller"},
	}}
	// The fallback engine allows everything; only the per-server policy
	// (loaded via GuardrailPolicy) denies this call — proving the
	// per-server row takes precedence over the fallback.
	rt := New(reg, oauthStore, nil, &fakeAuditEmitter{}, emptyFallbackEngine(t))

	req := httptest.NewRequest(http.MethodPost, "/v1/mcp/sql-tool/tools/call", bytes.NewBufferString(rpcToolsCallBody))
	req = req.WithContext(contextWithProjectID(t, mustProjectID(t)))
	req.Header.Set("Authorization", "Bearer "+rawToken)
	rec := httptest.NewRecorder()

	rt.ServeHTTP(rec, req)

	if upstreamHit {
		t.Fatal("upstream was hit despite the per-server guardrail policy denying the call")
	}
	code, msg := decodeRPCError(t, rec.Body.Bytes())
	if code != -32000 { // proxy.PolicyInterceptor's own "blocked by guardrail" code
		t.Fatalf("error code = %d, want -32000 (guardrail block); message=%q", code, msg)
	}
}

func TestRouterFallsBackToFallbackEngineWhenNoServerPolicyExists(t *testing.T) {
	upstreamHit := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit = true
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"ok"}`))
	}))
	defer upstream.Close()

	serverID := uuid.New()
	rawToken := "mcp_unguardedtoken"
	reg := &fakeRegistryStore{
		server:    registry.Server{ID: serverID, Name: "sql-tool", UpstreamURL: upstream.URL},
		hasPolicy: false, // no enabled guardrail_policies row
	}
	oauthStore := &fakeOAuthStore{byHash: map[string]oauth.TokenRecord{
		authkeys.Hash(rawToken): {MCPServerID: serverID.String(), CallerName: "caller"},
	}}
	rt := New(reg, oauthStore, nil, &fakeAuditEmitter{}, emptyFallbackEngine(t))

	// Same "DROP TABLE" payload that the per-server-policy test denies —
	// here it must be allowed through, since the fallback (empty) engine
	// blocks nothing.
	req := httptest.NewRequest(http.MethodPost, "/v1/mcp/sql-tool/tools/call", bytes.NewBufferString(rpcToolsCallBody))
	req = req.WithContext(contextWithProjectID(t, mustProjectID(t)))
	req.Header.Set("Authorization", "Bearer "+rawToken)
	rec := httptest.NewRecorder()

	rt.ServeHTTP(rec, req)

	if !upstreamHit {
		t.Fatal("upstream was not hit; fallback engine should have allowed this call")
	}
}

func TestRouterEnforcesPerServerRateLimit(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"ok"}`))
	}))
	defer upstream.Close()

	serverID := uuid.New()
	rawToken := "mcp_ratelimitedtoken"
	const dsl = "policies: []\nrate_limit:\n  requests_per_minute: 1\n"
	reg := &fakeRegistryStore{
		server:    registry.Server{ID: serverID, Name: "limited", UpstreamURL: upstream.URL},
		ruleDSL:   dsl,
		hasPolicy: true,
	}
	oauthStore := &fakeOAuthStore{byHash: map[string]oauth.TokenRecord{
		authkeys.Hash(rawToken): {MCPServerID: serverID.String(), CallerName: "caller"},
	}}
	rt := New(reg, oauthStore, newTestRedisLimiter(t), &fakeAuditEmitter{}, emptyFallbackEngine(t))

	newReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/v1/mcp/limited/tools/call", bytes.NewBufferString(rpcPingBody))
		req = req.WithContext(contextWithProjectID(t, mustProjectID(t)))
		req.Header.Set("Authorization", "Bearer "+rawToken)
		return req
	}

	rec1 := httptest.NewRecorder()
	rt.ServeHTTP(rec1, newReq())
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", rec1.Code)
	}
	var firstResp struct {
		Error *struct{ Code int } `json:"error"`
	}
	json.Unmarshal(rec1.Body.Bytes(), &firstResp)
	if firstResp.Error != nil {
		t.Fatalf("first request under the limit was rejected: %+v", firstResp.Error)
	}

	rec2 := httptest.NewRecorder()
	rt.ServeHTTP(rec2, newReq())
	code, _ := decodeRPCError(t, rec2.Body.Bytes())
	if code != codeRateLimited {
		t.Fatalf("second request error code = %d, want %d (rate limit exceeded)", code, codeRateLimited)
	}
}

func TestRouterSkipsRateLimitingWhenLimiterIsNil(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"ok"}`))
	}))
	defer upstream.Close()

	serverID := uuid.New()
	rawToken := "mcp_nolimiterdeployed"
	const dsl = "policies: []\nrate_limit:\n  requests_per_minute: 1\n"
	reg := &fakeRegistryStore{
		server:    registry.Server{ID: serverID, Name: "limited", UpstreamURL: upstream.URL},
		ruleDSL:   dsl,
		hasPolicy: true,
	}
	oauthStore := &fakeOAuthStore{byHash: map[string]oauth.TokenRecord{
		authkeys.Hash(rawToken): {MCPServerID: serverID.String(), CallerName: "caller"},
	}}
	// limiter is nil — e.g. Redis was unavailable at startup — so even a
	// policy configuring rate_limit: 1/min must never block traffic.
	rt := New(reg, oauthStore, nil, &fakeAuditEmitter{}, emptyFallbackEngine(t))

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/mcp/limited/tools/call", bytes.NewBufferString(rpcPingBody))
		req = req.WithContext(contextWithProjectID(t, mustProjectID(t)))
		req.Header.Set("Authorization", "Bearer "+rawToken)
		rec := httptest.NewRecorder()

		rt.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200 (nil limiter must never block)", i+1, rec.Code)
		}
	}
}

func TestRouterFailsOpenWhenRedisIsUnavailable(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"ok"}`))
	}))
	defer upstream.Close()

	// A Limiter pointed at an address nothing is listening on: Allow
	// will return an error, and the router must fail OPEN (allow the
	// call) rather than block all traffic because Redis hiccuped — a
	// rate limiter is an anti-abuse guardrail, not a correctness gate.
	deadClient := goredis.NewClient(&goredis.Options{
		Addr:        "127.0.0.1:1", // nothing listens on port 1
		DialTimeout: 200 * time.Millisecond,
		MaxRetries:  -1, // fail fast instead of go-redis's default retry-with-backoff
	})
	t.Cleanup(func() { deadClient.Close() })
	limiter := ratelimit.New(deadClient)

	serverID := uuid.New()
	rawToken := "mcp_deadredistoken"
	const dsl = "policies: []\nrate_limit:\n  requests_per_minute: 1\n"
	reg := &fakeRegistryStore{
		server:    registry.Server{ID: serverID, Name: "limited", UpstreamURL: upstream.URL},
		ruleDSL:   dsl,
		hasPolicy: true,
	}
	oauthStore := &fakeOAuthStore{byHash: map[string]oauth.TokenRecord{
		authkeys.Hash(rawToken): {MCPServerID: serverID.String(), CallerName: "caller"},
	}}
	rt := New(reg, oauthStore, limiter, &fakeAuditEmitter{}, emptyFallbackEngine(t))

	req := httptest.NewRequest(http.MethodPost, "/v1/mcp/limited/tools/call", bytes.NewBufferString(rpcPingBody))
	req = req.WithContext(contextWithProjectID(t, mustProjectID(t)))
	req.Header.Set("Authorization", "Bearer "+rawToken)
	rec := httptest.NewRecorder()

	rt.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Error *struct{ Code int } `json:"error"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Error != nil {
		t.Fatalf("request was denied on a Redis outage, want fail-open: %+v", resp.Error)
	}
}

// --- pure helper-function tests ------------------------------------------

func TestParseServerPath(t *testing.T) {
	cases := []struct {
		path     string
		wantName string
		wantSub  string
		wantOK   bool
	}{
		{"/v1/mcp/weather", "weather", "/", true},
		{"/v1/mcp/weather/", "weather", "/", true},
		{"/v1/mcp/weather/tools/call", "weather", "/tools/call", true},
		{"/v1/mcp/", "", "", false},
		{"/v1/mcp", "", "", false},
		{"/other/path", "", "", false},
	}
	for _, c := range cases {
		name, sub, ok := parseServerPath(c.path)
		if ok != c.wantOK {
			t.Errorf("parseServerPath(%q) ok = %v, want %v", c.path, ok, c.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if name != c.wantName || sub != c.wantSub {
			t.Errorf("parseServerPath(%q) = (%q, %q), want (%q, %q)", c.path, name, sub, c.wantName, c.wantSub)
		}
	}
}

func TestBearerToken(t *testing.T) {
	cases := []struct {
		header string
		want   string
	}{
		{"", ""},
		{"Bearer mcp_abc123", "mcp_abc123"},
		{"bearer mcp_abc123", "mcp_abc123"},
		{"Basic dXNlcjpwYXNz", ""},
		{"Bearer", ""},
	}
	for _, c := range cases {
		if got := bearerToken(c.header); got != c.want {
			t.Errorf("bearerToken(%q) = %q, want %q", c.header, got, c.want)
		}
	}
}
