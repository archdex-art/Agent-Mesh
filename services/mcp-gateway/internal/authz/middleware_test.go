package authz

import (
	"context"
	"encoding/json"
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
	store := &fakeStore{record: authkeys.Record{ProjectID: projectID, Role: authkeys.RoleIngest}}

	var gotProjectID ids.ProjectID
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := ProjectIDFromContext(r.Context())
		if err != nil {
			t.Fatalf("ProjectIDFromContext: %v", err)
		}
		gotProjectID = id
		w.WriteHeader(http.StatusOK)
	})

	handler := Middleware(store)(inner)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set(APIKeyHeader, "am_live_validkey1234567890")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if gotProjectID != projectID {
		t.Fatalf("ProjectID = %v, want %v", gotProjectID, projectID)
	}
}

func TestMiddlewareRejectsMissingKeyWithJSONRPCError(t *testing.T) {
	store := &fakeStore{}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner handler must not run for a missing API key")
	})

	handler := Middleware(store)(inner)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// JSON-RPC transport convention: HTTP 200, error carried in the body.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (JSON-RPC errors ride at HTTP 200)", rec.Code, http.StatusOK)
	}

	var body struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON-RPC error shape: %v", err)
	}
	if body.Error.Code != -32001 {
		t.Fatalf("error.code = %d, want -32001", body.Error.Code)
	}
}

func TestMiddlewareRejectsInvalidKey(t *testing.T) {
	store := &fakeStore{err: amerrors.New(amerrors.CodeNotFound, "not found")}
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })

	handler := Middleware(store)(inner)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set(APIKeyHeader, "am_live_wrongkey1234567")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if called {
		t.Fatal("inner handler must not run for an invalid API key")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestProjectIDFromContextErrorsWithoutMiddleware(t *testing.T) {
	_, err := ProjectIDFromContext(context.Background())
	if err == nil {
		t.Fatal("ProjectIDFromContext succeeded without middleware, want error")
	}
	if amerrors.CodeOf(err) != amerrors.CodeInternal {
		t.Fatalf("CodeOf(err) = %v, want CodeInternal", amerrors.CodeOf(err))
	}
}
