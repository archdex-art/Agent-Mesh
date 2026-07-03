// Package authclient talks to the Query API's account-management REST
// surface: POST /v1/auth/login, GET /v1/auth/projects, and POST
// /v1/auth/projects (`agentmesh login`'s backing calls). Like
// cli/internal/registryclient, this package declares its own
// request/response wire types rather than importing any server-side
// module — the CLI is a separately distributed binary that only depends
// on the documented HTTP contract, not on the Query API's internal Go
// types.
//
// This is a NEW, ADDITIONAL auth layer on top of the existing API-key
// mechanism registryclient and tailclient use. Every function here
// authenticates with a session token via the "Authorization: Bearer
// <token>" header — deliberately never the "X-AgentMesh-API-Key" header
// those packages use — so the two credentials can never be confused in a
// request: a session token proves "this is a logged-in human," an API
// key proves "this request may read/write one project's trace data."
package authclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// httpClient is package-level so `agentmesh login` never hangs
// indefinitely against an unreachable Query API — same rationale as
// registryclient's package-level client.
var httpClient = &http.Client{Timeout: 15 * time.Second}

// loginRequest is the JSON body for POST /v1/auth/login.
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// loginResponse is the 200 response body from POST /v1/auth/login.
type loginResponse struct {
	SessionToken string `json:"session_token"`
	UserID       string `json:"user_id"`
}

// Login authenticates email/password against the Query API and, on
// success, returns a new opaque session token (the credential every
// other function in this package requires) plus the caller's user id.
// A wrong password and an unknown email both surface as the same
// generic "invalid email or password" error in the response body — the
// Query API deliberately never reveals which of the two was wrong, and
// this function does not attempt to distinguish them either.
func Login(queryAPIURL, email, password string) (sessionToken, userID string, err error) {
	body, err := json.Marshal(loginRequest{Email: email, Password: password})
	if err != nil {
		return "", "", fmt.Errorf("encoding login request: %w", err)
	}

	endpoint := strings.TrimRight(queryAPIURL, "/") + "/v1/auth/login"
	httpReq, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("building request to %s: %w", endpoint, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return "", "", fmt.Errorf("calling Query API at %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", "", fmt.Errorf("reading login response from %s: %w", endpoint, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("login failed (status %d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var out loginResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", "", fmt.Errorf("decoding login response from %s: %w (body: %s)", endpoint, err, strings.TrimSpace(string(respBody)))
	}
	return out.SessionToken, out.UserID, nil
}

// ProjectSummary is one entry of GET /v1/auth/projects's response: a
// project the caller owns, plus its first active API key's display
// prefix only. The Query API deliberately never re-exposes a raw key
// after creation, so this endpoint — and therefore this struct — can
// never carry enough information to recover an existing project's key;
// cmd/login.go explains that constraint to the user rather than working
// around it.
type ProjectSummary struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	CreatedAt    string `json:"created_at"`
	APIKeyPrefix string `json:"api_key_prefix"`
}

// ListProjects returns every project owned by the user identified by
// sessionToken, authenticating via the "Authorization: Bearer" header —
// the session-auth counterpart to registryclient.Register's
// "X-AgentMesh-API-Key" header.
func ListProjects(queryAPIURL, sessionToken string) ([]ProjectSummary, error) {
	endpoint := strings.TrimRight(queryAPIURL, "/") + "/v1/auth/projects"
	httpReq, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("building request to %s: %w", endpoint, err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+sessionToken)

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("calling Query API at %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return nil, fmt.Errorf("reading projects response from %s: %w", endpoint, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("listing projects failed (status %d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var out []ProjectSummary
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("decoding projects response from %s: %w (body: %s)", endpoint, err, strings.TrimSpace(string(respBody)))
	}
	return out, nil
}

// createProjectRequest is the JSON body for POST /v1/auth/projects. Name
// is optional — an empty value is omitted from the encoded body so the
// Query API applies its own "Project <uuid8>" default (setup.go's
// existing naming convention), the same contract POST /v1/setup already
// has for an omitted name.
type createProjectRequest struct {
	Name string `json:"name,omitempty"`
}

// createProjectResponse is the 201 response body from POST
// /v1/auth/projects.
type createProjectResponse struct {
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	APIKey    string `json:"api_key"`
}

// CreateProject creates a new project owned by the session's user, plus
// its first API key, in one call — mirroring POST /v1/setup's response
// shape for the key: returned raw, once, here and nowhere else. name may
// be empty to accept the Query API's default naming.
func CreateProject(queryAPIURL, sessionToken, name string) (projectID, apiKey string, err error) {
	body, err := json.Marshal(createProjectRequest{Name: name})
	if err != nil {
		return "", "", fmt.Errorf("encoding create-project request: %w", err)
	}

	endpoint := strings.TrimRight(queryAPIURL, "/") + "/v1/auth/projects"
	httpReq, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("building request to %s: %w", endpoint, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+sessionToken)

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return "", "", fmt.Errorf("calling Query API at %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", "", fmt.Errorf("reading create-project response from %s: %w", endpoint, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("creating project failed (status %d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var out createProjectResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", "", fmt.Errorf("decoding create-project response from %s: %w (body: %s)", endpoint, err, strings.TrimSpace(string(respBody)))
	}
	return out.ProjectID, out.APIKey, nil
}
