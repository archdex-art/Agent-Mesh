package proxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentmesh/agentmesh/services/mcp-gateway/internal/audit"
	"github.com/agentmesh/agentmesh/services/mcp-gateway/internal/authz"
	"github.com/agentmesh/agentmesh/shared/authkeys"
	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/agentmesh/agentmesh/shared/span"
)

type fakeInterceptor struct {
	err error
}

func (f *fakeInterceptor) Intercept(ctx context.Context, req MCPRPCRequest) error {
	return f.err
}

type fakeEmitter struct {
	calls []audit.Call
	err   error
}

func (f *fakeEmitter) Emit(ctx context.Context, call audit.Call) error {
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, call)
	return nil
}

type staticAuthStore struct {
	record authkeys.Record
}

func (s *staticAuthStore) LookupByHash(ctx context.Context, hashedKey string) (authkeys.Record, error) {
	return s.record, nil
}

func mustProjectIDForAudit(t *testing.T) ids.ProjectID {
	t.Helper()
	id, err := ids.NewProjectID()
	if err != nil {
		t.Fatalf("NewProjectID: %v", err)
	}
	return id
}

// contextWithProjectID drives a real authz.Middleware pass to obtain a
// context carrying the authenticated ProjectID — since authz's context
// key type is unexported by design, this is the correct way to construct
// such a context from outside the authz package (matching how the real
// HTTP handler chain would populate it), rather than reaching into authz's
// internals.
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

func TestAuditingInterceptorEmitsOnAllow(t *testing.T) {
	projectID := mustProjectIDForAudit(t)
	ctx := contextWithProjectID(t, projectID)

	inner := &fakeInterceptor{err: nil}
	emitter := &fakeEmitter{}
	auditing := NewAuditingInterceptor(inner, emitter)

	req := MCPRPCRequest{
		Method: "tools/call",
		Params: []byte(`{"name": "web_search", "arguments": {}}`),
	}

	err := auditing.Intercept(ctx, req)
	if err != nil {
		t.Fatalf("Intercept: %v", err)
	}
	if len(emitter.calls) != 1 {
		t.Fatalf("emitter received %d calls, want 1", len(emitter.calls))
	}
	if emitter.calls[0].Status != span.StatusOK {
		t.Errorf("Status = %v, want %v", emitter.calls[0].Status, span.StatusOK)
	}
	if emitter.calls[0].ToolName != "web_search" {
		t.Errorf("ToolName = %q, want %q", emitter.calls[0].ToolName, "web_search")
	}
	if emitter.calls[0].ProjectID != projectID {
		t.Errorf("ProjectID = %v, want %v", emitter.calls[0].ProjectID, projectID)
	}
}

func TestAuditingInterceptorEmitsOnDenyWithReason(t *testing.T) {
	projectID := mustProjectIDForAudit(t)
	ctx := contextWithProjectID(t, projectID)

	denyErr := errors.New(`policy "prevent_destructive_sql" denied tool "execute_query"`)
	inner := &fakeInterceptor{err: denyErr}
	emitter := &fakeEmitter{}
	auditing := NewAuditingInterceptor(inner, emitter)

	req := MCPRPCRequest{
		Method: "tools/call",
		Params: []byte(`{"name": "execute_query", "arguments": {"sql": "DROP TABLE users"}}`),
	}

	err := auditing.Intercept(ctx, req)
	if err == nil {
		t.Fatal("Intercept() succeeded despite inner denial, want the denial propagated")
	}
	if len(emitter.calls) != 1 {
		t.Fatalf("emitter received %d calls, want 1", len(emitter.calls))
	}
	if emitter.calls[0].Status != span.StatusDenied {
		t.Errorf("Status = %v, want %v", emitter.calls[0].Status, span.StatusDenied)
	}
	if emitter.calls[0].DenyReason == "" {
		t.Error("DenyReason is empty, want the inner error message")
	}
}

func TestAuditingInterceptorSkipsNonToolCallMethods(t *testing.T) {
	projectID := mustProjectIDForAudit(t)
	ctx := contextWithProjectID(t, projectID)

	inner := &fakeInterceptor{err: nil}
	emitter := &fakeEmitter{}
	auditing := NewAuditingInterceptor(inner, emitter)

	req := MCPRPCRequest{Method: "initialize"}
	if err := auditing.Intercept(ctx, req); err != nil {
		t.Fatalf("Intercept: %v", err)
	}
	if len(emitter.calls) != 0 {
		t.Fatalf("emitter received %d calls for a non-tools/call method, want 0", len(emitter.calls))
	}
}

func TestAuditingInterceptorDoesNotFailRequestWhenEmitFails(t *testing.T) {
	projectID := mustProjectIDForAudit(t)
	ctx := contextWithProjectID(t, projectID)

	inner := &fakeInterceptor{err: nil}
	emitter := &fakeEmitter{err: errors.New("collector unreachable")}
	auditing := NewAuditingInterceptor(inner, emitter)

	req := MCPRPCRequest{Method: "tools/call", Params: []byte(`{"name": "search"}`)}
	err := auditing.Intercept(ctx, req)
	if err != nil {
		t.Fatalf("Intercept returned an error when only audit emission failed, want the proxied call to still succeed: %v", err)
	}
}

func TestAuditingInterceptorSkipsEmissionWithoutProjectInContext(t *testing.T) {
	inner := &fakeInterceptor{err: nil}
	emitter := &fakeEmitter{}
	auditing := NewAuditingInterceptor(inner, emitter)

	req := MCPRPCRequest{Method: "tools/call", Params: []byte(`{"name": "search"}`)}
	err := auditing.Intercept(context.Background(), req) // no authenticated project in context
	if err != nil {
		t.Fatalf("Intercept: %v", err)
	}
	if len(emitter.calls) != 0 {
		t.Fatalf("emitter received %d calls without an authenticated project, want 0", len(emitter.calls))
	}
}
