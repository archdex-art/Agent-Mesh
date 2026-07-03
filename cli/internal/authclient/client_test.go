package authclient

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoginSendsBodyThenParses200(t *testing.T) {
	var (
		gotMethod  string
		gotPath    string
		gotContent string
		gotBody    loginRequest
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContent = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("server: decoding request body: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(loginResponse{
			SessionToken: "ams_test-session-token",
			UserID:       "11111111-1111-1111-1111-111111111111",
		})
	}))
	defer server.Close()

	sessionToken, userID, err := Login(server.URL, "person@example.com", "hunter22")
	if err != nil {
		t.Fatalf("Login: unexpected error: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/auth/login" {
		t.Errorf("path = %q, want /v1/auth/login", gotPath)
	}
	if !strings.HasPrefix(gotContent, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", gotContent)
	}
	if gotBody.Email != "person@example.com" || gotBody.Password != "hunter22" {
		t.Errorf("request body = %+v, want email/password echoed verbatim", gotBody)
	}

	if sessionToken != "ams_test-session-token" {
		t.Errorf("sessionToken = %q, want the server-issued token", sessionToken)
	}
	if userID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("userID = %q, want the server-issued uuid", userID)
	}
}

func TestLoginTrimsTrailingSlashFromQueryAPIURL(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(loginResponse{SessionToken: "t", UserID: "u"})
	}))
	defer server.Close()

	if _, _, err := Login(server.URL+"/", "a@b.com", "password1"); err != nil {
		t.Fatalf("Login: unexpected error: %v", err)
	}
	if gotPath != "/v1/auth/login" {
		t.Errorf("path = %q, want /v1/auth/login (no double slash)", gotPath)
	}
}

func TestLoginSurfacesErrorBodyOn401Unauthenticated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthenticated","message":"invalid email or password"}}`))
	}))
	defer server.Close()

	_, _, err := Login(server.URL, "nope@example.com", "wrongpass")
	if err == nil {
		t.Fatal("Login: expected an error on 401, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error %q should mention the 401 status code", err.Error())
	}
	if !strings.Contains(err.Error(), "invalid email or password") {
		t.Errorf("error %q should include the response body for debuggability", err.Error())
	}
}

func TestListProjectsSendsBearerHeaderThenParsesArray(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotAuth   string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode([]ProjectSummary{
			{ID: "p1", Name: "First Project", CreatedAt: "2026-01-01T00:00:00Z", APIKeyPrefix: "am_live_ab12"},
			{ID: "p2", Name: "Second Project", CreatedAt: "2026-02-01T00:00:00Z", APIKeyPrefix: "am_live_cd34"},
		})
	}))
	defer server.Close()

	projects, err := ListProjects(server.URL, "ams_test-session-token")
	if err != nil {
		t.Fatalf("ListProjects: unexpected error: %v", err)
	}

	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/v1/auth/projects" {
		t.Errorf("path = %q, want /v1/auth/projects", gotPath)
	}
	if gotAuth != "Bearer ams_test-session-token" {
		t.Errorf("Authorization header = %q, want Bearer ams_test-session-token", gotAuth)
	}

	if len(projects) != 2 {
		t.Fatalf("len(projects) = %d, want 2", len(projects))
	}
	if projects[0].ID != "p1" || projects[0].APIKeyPrefix != "am_live_ab12" {
		t.Errorf("projects[0] = %+v, want the first server-issued project", projects[0])
	}
	if projects[1].Name != "Second Project" {
		t.Errorf("projects[1].Name = %q, want Second Project", projects[1].Name)
	}
}

func TestListProjectsReturnsEmptySliceForNoProjects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode([]ProjectSummary{})
	}))
	defer server.Close()

	projects, err := ListProjects(server.URL, "ams_test-session-token")
	if err != nil {
		t.Fatalf("ListProjects: unexpected error: %v", err)
	}
	if len(projects) != 0 {
		t.Errorf("len(projects) = %d, want 0", len(projects))
	}
}

func TestListProjectsSurfacesErrorBodyOn401(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthenticated","message":"session expired or revoked"}}`))
	}))
	defer server.Close()

	_, err := ListProjects(server.URL, "ams_expired-token")
	if err == nil {
		t.Fatal("ListProjects: expected an error on 401, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error %q should mention the 401 status code", err.Error())
	}
	if !strings.Contains(err.Error(), "session expired or revoked") {
		t.Errorf("error %q should include the response body for debuggability", err.Error())
	}
}

func TestCreateProjectSendsBearerHeaderAndBodyThenParses201(t *testing.T) {
	var (
		gotMethod  string
		gotPath    string
		gotAuth    string
		gotContent string
		gotBody    createProjectRequest
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContent = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("server: decoding request body: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(createProjectResponse{
			ProjectID: "22222222-2222-2222-2222-222222222222",
			Name:      gotBody.Name,
			APIKey:    "am_live_freshkey123",
		})
	}))
	defer server.Close()

	projectID, apiKey, err := CreateProject(server.URL, "ams_test-session-token", "My Project")
	if err != nil {
		t.Fatalf("CreateProject: unexpected error: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/auth/projects" {
		t.Errorf("path = %q, want /v1/auth/projects", gotPath)
	}
	if gotAuth != "Bearer ams_test-session-token" {
		t.Errorf("Authorization header = %q, want Bearer ams_test-session-token", gotAuth)
	}
	if !strings.HasPrefix(gotContent, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", gotContent)
	}
	if gotBody.Name != "My Project" {
		t.Errorf("request body Name = %q, want My Project", gotBody.Name)
	}

	if projectID != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("projectID = %q, want the server-issued uuid", projectID)
	}
	if apiKey != "am_live_freshkey123" {
		t.Errorf("apiKey = %q, want the server-issued raw key", apiKey)
	}
}

func TestCreateProjectOmitsNameFieldWhenBlank(t *testing.T) {
	var gotRawBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotRawBody = string(raw)

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(createProjectResponse{
			ProjectID: "p", Name: "Project abcdef01", APIKey: "am_live_x",
		})
	}))
	defer server.Close()

	if _, _, err := CreateProject(server.URL, "ams_token", ""); err != nil {
		t.Fatalf("CreateProject: unexpected error: %v", err)
	}
	if strings.Contains(gotRawBody, "name") {
		t.Errorf("request body %q should omit the name field entirely for a blank name (so the server default applies)", gotRawBody)
	}
}

func TestCreateProjectSurfacesErrorBodyOn409Conflict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"a project named \"Duplicate\" already exists"}`))
	}))
	defer server.Close()

	_, _, err := CreateProject(server.URL, "ams_token", "Duplicate")
	if err == nil {
		t.Fatal("CreateProject: expected an error on 409, got nil")
	}
	if !strings.Contains(err.Error(), "409") {
		t.Errorf("error %q should mention the 409 status code", err.Error())
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error %q should include the response body for debuggability", err.Error())
	}
}
