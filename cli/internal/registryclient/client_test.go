package registryclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegisterSendsHeadersAndBodyThenParses201(t *testing.T) {
	var (
		gotMethod  string
		gotPath    string
		gotAPIKey  string
		gotContent string
		gotBody    RegisterRequest
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("X-AgentMesh-API-Key")
		gotContent = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("server: decoding request body: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(RegisterResponse{
			ID:   "11111111-1111-1111-1111-111111111111",
			Name: gotBody.Name,
		})
	}))
	defer server.Close()

	req := RegisterRequest{
		Name:         "mock-crm",
		UpstreamURL:  "http://localhost:9090",
		Transport:    "streamable-http",
		Version:      "1.0.0",
		Owner:        "platform-team",
		ManifestYAML: "name: mock-crm\n",
	}

	resp, err := Register(server.URL, "am_live_test-key", req)
	if err != nil {
		t.Fatalf("Register: unexpected error: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/mcp/servers" {
		t.Errorf("path = %q, want /v1/mcp/servers", gotPath)
	}
	if gotAPIKey != "am_live_test-key" {
		t.Errorf("X-AgentMesh-API-Key header = %q, want am_live_test-key", gotAPIKey)
	}
	if !strings.HasPrefix(gotContent, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", gotContent)
	}
	if gotBody != req {
		t.Errorf("request body = %+v, want %+v", gotBody, req)
	}

	if resp.ID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("resp.ID = %q, want the server-issued uuid", resp.ID)
	}
	if resp.Name != "mock-crm" {
		t.Errorf("resp.Name = %q, want mock-crm", resp.Name)
	}
}

func TestRegisterTrimsTrailingSlashFromQueryAPIURL(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(RegisterResponse{ID: "x", Name: "y"})
	}))
	defer server.Close()

	if _, err := Register(server.URL+"/", "key", RegisterRequest{Name: "y"}); err != nil {
		t.Fatalf("Register: unexpected error: %v", err)
	}
	if gotPath != "/v1/mcp/servers" {
		t.Errorf("path = %q, want /v1/mcp/servers (no double slash)", gotPath)
	}
}

func TestRegisterSurfacesErrorBodyOn409Conflict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"a server named \"mock-crm\" already exists in this project"}`))
	}))
	defer server.Close()

	_, err := Register(server.URL, "key", RegisterRequest{Name: "mock-crm"})
	if err == nil {
		t.Fatal("Register: expected an error on 409, got nil")
	}
	if !strings.Contains(err.Error(), "409") {
		t.Errorf("error %q should mention the 409 status code", err.Error())
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error %q should include the response body for debuggability", err.Error())
	}
}

func TestRegisterSurfacesErrorBodyOn400BadRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"transport must be one of stdio, streamable-http"}`))
	}))
	defer server.Close()

	_, err := Register(server.URL, "key", RegisterRequest{Name: "bad-server", Transport: "carrier-pigeon"})
	if err == nil {
		t.Fatal("Register: expected an error on 400, got nil")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error %q should mention the 400 status code", err.Error())
	}
	if !strings.Contains(err.Error(), "transport must be one of") {
		t.Errorf("error %q should include the response body for debuggability", err.Error())
	}
}
