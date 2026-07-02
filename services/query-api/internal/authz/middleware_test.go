package authz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentmesh/agentmesh/shared/authkeys"
	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
)

type fakeStore struct {
	record authkeys.Record
	err    error
}

func (f *fakeStore) LookupByHash(ctx context.Context, hashedKey string) (authkeys.Record, error) {
	if f.err != nil {
		return authkeys.Record{}, f.err
	}
	return f.record, nil
}

func mustProjectID(t *testing.T) ids.ProjectID {
	t.Helper()
	id, err := ids.NewProjectID()
	if err != nil {
		t.Fatalf("NewProjectID: %v", err)
	}
	return id
}

func TestMiddlewareAllowsValidKeyThrough(t *testing.T) {
	projectID := mustProjectID(t)
	store := &fakeStore{record: authkeys.Record{ProjectID: projectID, Role: authkeys.RoleRead}}

	var gotProjectID ids.ProjectID
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		record, err := RecordFromContext(r.Context())
		if err != nil {
			t.Fatalf("RecordFromContext: %v", err)
		}
		gotProjectID = record.ProjectID
		w.WriteHeader(http.StatusOK)
	})

	handler := Middleware(store)(inner)
	req := httptest.NewRequest(http.MethodGet, "/v1/traces", nil)
	req.Header.Set(apiKeyHeader, "am_live_validkey1234567890")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if gotProjectID != projectID {
		t.Fatalf("ProjectID in context = %v, want %v", gotProjectID, projectID)
	}
}

func TestMiddlewareRejectsMissingKey(t *testing.T) {
	store := &fakeStore{}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner handler should not be called for a missing API key")
	})

	handler := Middleware(store)(inner)
	req := httptest.NewRequest(http.MethodGet, "/v1/traces", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestMiddlewareRejectsInvalidKey(t *testing.T) {
	store := &fakeStore{err: amerrors.New(amerrors.CodeNotFound, "not found")}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner handler should not be called for an invalid API key")
	})

	handler := Middleware(store)(inner)
	req := httptest.NewRequest(http.MethodGet, "/v1/traces", nil)
	req.Header.Set(apiKeyHeader, "am_live_wrongkey12345678")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestRecordFromContextErrorsWithoutMiddleware(t *testing.T) {
	_, err := RecordFromContext(context.Background())
	if err == nil {
		t.Fatal("RecordFromContext succeeded on a context with no record, want error")
	}
	if amerrors.CodeOf(err) != amerrors.CodeInternal {
		t.Fatalf("CodeOf(err) = %v, want CodeInternal", amerrors.CodeOf(err))
	}
}

func TestProjectIDFromRequestDelegatesToContext(t *testing.T) {
	projectID := mustProjectID(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/traces", nil)
	ctx := context.WithValue(req.Context(), contextKey{}, authkeys.Record{ProjectID: projectID})
	req = req.WithContext(ctx)

	got, err := ProjectIDFromRequest(req)
	if err != nil {
		t.Fatalf("ProjectIDFromRequest: %v", err)
	}
	if got != projectID {
		t.Fatalf("ProjectIDFromRequest = %v, want %v", got, projectID)
	}
}

func TestProjectIDFromRequestErrorsWithoutMiddleware(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/traces", nil)
	_, err := ProjectIDFromRequest(req)
	if err == nil {
		t.Fatal("ProjectIDFromRequest succeeded without middleware, want error")
	}
}
