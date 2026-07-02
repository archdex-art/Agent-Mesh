// MCP Registry REST surface (Architecture.md §2's "MCP Registry: part of
// Query API surface", §10's `agentmesh mcp register` CLI contract, and
// System Design.md §2.2's `mcp_servers`/`guardrail_policies` schema
// sketch). This is the write/read path that lets an operator register an
// MCP server with the Gateway instead of the Gateway pointing at one
// hardcoded upstream (Milestone 6's "Registry so the Gateway can route to
// many registered servers").
//
// The Gateway (services/mcp-gateway) reads the same `mcp_servers` /
// `guardrail_policies` / `mcp_server_tokens` tables directly against its
// own Postgres connection rather than calling back into this handler —
// the two services independently read one shared Postgres schema, exactly
// like the Collector and Query API already independently read `spans` in
// ClickHouse (Architecture.md §2's "service boundaries drawn along data
// ownership, not team ownership").
package rest

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/agentmesh/agentmesh/shared/authkeys"
	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// uuidPattern validates a path-supplied id is at least syntactically a
// UUID before it ever reaches a query bound to a `uuid`-typed column —
// otherwise a malformed id surfaces as a Postgres "invalid input syntax
// for type uuid" query error indistinguishable from a real outage. Same
// fail-fast-with-400 philosophy as this file's transport validation.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// validTransports mirrors schema/postgres/004_mcp_registry.sql's
// `transport` CHECK constraint exactly, so a bad value is rejected here
// with a clear 400 instead of a raw constraint-violation error leaking to
// the caller.
var validTransports = map[string]bool{
	"stdio":           true,
	"streamable-http": true,
}

// MCPServerView is the JSON response shape for a registered server —
// kept distinct from the `mcp_servers` row so the wire format (string
// timestamp, no internal-only columns) can evolve independently of the
// table, same rationale as traces.go's SpanView.
type MCPServerView struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	UpstreamURL  string `json:"upstream_url"`
	Transport    string `json:"transport"`
	Version      string `json:"version"`
	Owner        string `json:"owner"`
	ManifestYAML string `json:"manifest_yaml"`
	CreatedAt    string `json:"created_at"`
}

// createServerRequest is the POST /v1/mcp/servers request body. RuleDSL is
// optional: a server can be registered without a guardrail policy and
// have one attached later.
type createServerRequest struct {
	Name         string `json:"name"`
	UpstreamURL  string `json:"upstream_url"`
	Transport    string `json:"transport"`
	Version      string `json:"version"`
	Owner        string `json:"owner"`
	ManifestYAML string `json:"manifest_yaml"`
	RuleDSL      string `json:"rule_dsl"`
}

// createTokenRequest is the POST /v1/mcp/servers/{id}/tokens request body.
type createTokenRequest struct {
	CallerName string `json:"caller_name"`
}

// MCPRegistryHandler serves the Registry's REST surface: create/list/
// get/delete for `mcp_servers` plus bearer-token issuance for
// `mcp_server_tokens`. It talks to Postgres directly via *pgxpool.Pool
// (no separate store/reader abstraction) — mirroring SetupHandler, the
// existing precedent for a handler this size that owns transactional
// writes, rather than TracesHandler's interface-backed pattern, which
// exists to swap ClickHouse for a fake in tests, not something this
// handler needs at its scale.
type MCPRegistryHandler struct {
	pool      *pgxpool.Pool
	projectID func(r *http.Request) (ids.ProjectID, error)
}

// NewMCPRegistryHandler returns a handler backed by pool. projectIDFromRequest
// is injected rather than hardcoded so this handler stays independent of
// the specific auth transport, same rationale as NewTracesHandler.
func NewMCPRegistryHandler(pool *pgxpool.Pool, projectIDFromRequest func(r *http.Request) (ids.ProjectID, error)) *MCPRegistryHandler {
	return &MCPRegistryHandler{pool: pool, projectID: projectIDFromRequest}
}

// ServeHTTP dispatches on method + path shape, the same single-handler
// convention TracesHandler.ServeHTTP uses for /v1/traces vs
// /v1/traces/{id}: one mux registration in cmd/main.go, distinct response
// shapes kept as separate types above.
//
//	POST   /v1/mcp/servers            -> createServer
//	GET    /v1/mcp/servers            -> listServers
//	GET    /v1/mcp/servers/{id}       -> getServer
//	DELETE /v1/mcp/servers/{id}       -> deleteServer
//	POST   /v1/mcp/servers/{id}/tokens -> createToken
func (h *MCPRegistryHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	projectID, err := h.projectID(r)
	if err != nil {
		writeError(w, err, http.StatusUnauthorized)
		return
	}

	trimmed := strings.TrimPrefix(r.URL.Path, "/v1/mcp/servers")
	if trimmed == r.URL.Path {
		writeError(w, amerrors.New(amerrors.CodeNotFound, "not found"), http.StatusNotFound)
		return
	}
	trimmed = strings.Trim(trimmed, "/")

	if trimmed == "" {
		switch r.Method {
		case http.MethodPost:
			h.createServer(w, r, projectID)
		case http.MethodGet:
			h.listServers(w, r, projectID)
		default:
			writeError(w, amerrors.New(amerrors.CodeInvalidArgument, "method not allowed"), http.StatusMethodNotAllowed)
		}
		return
	}

	parts := strings.SplitN(trimmed, "/", 2)
	serverIDStr := parts[0]
	if !uuidPattern.MatchString(serverIDStr) {
		writeError(w, amerrors.New(amerrors.CodeInvalidArgument, "invalid mcp server id"), http.StatusBadRequest)
		return
	}

	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		h.getServer(w, r, projectID, serverIDStr)
	case len(parts) == 1 && r.Method == http.MethodDelete:
		h.deleteServer(w, r, projectID, serverIDStr)
	case len(parts) == 2 && parts[1] == "tokens" && r.Method == http.MethodPost:
		h.createToken(w, r, projectID, serverIDStr)
	default:
		writeError(w, amerrors.New(amerrors.CodeInvalidArgument, "method not allowed"), http.StatusMethodNotAllowed)
	}
}

// createServer inserts a new mcp_servers row and, if rule_dsl was
// supplied, an enabled guardrail_policies row in the same transaction —
// mirroring SetupHandler's transactional project+api_key insert pattern
// exactly, since both are "create N related control-plane rows atomically
// or none of them" operations.
func (h *MCPRegistryHandler) createServer(w http.ResponseWriter, r *http.Request, projectID ids.ProjectID) {
	var req createServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, amerrors.Wrap(amerrors.CodeInvalidArgument, "decoding request body", err), http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.UpstreamURL == "" || req.Version == "" || req.Owner == "" || req.ManifestYAML == "" {
		writeError(w, amerrors.New(amerrors.CodeInvalidArgument, "name, upstream_url, transport, version, owner, and manifest_yaml are required"), http.StatusBadRequest)
		return
	}
	if !validTransports[req.Transport] {
		writeError(w, amerrors.New(amerrors.CodeInvalidArgument, `transport must be "stdio" or "streamable-http"`), http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		writeStoreError(w, amerrors.Wrap(amerrors.CodeUnavailable, "starting tx", err))
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once Commit has succeeded; the return value has no recovery action either way

	var serverID string
	var createdAt time.Time
	err = tx.QueryRow(ctx, `
		INSERT INTO mcp_servers (project_id, name, upstream_url, transport, version, owner, manifest_yaml)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at
	`, projectID.String(), req.Name, req.UpstreamURL, req.Transport, req.Version, req.Owner, req.ManifestYAML,
	).Scan(&serverID, &createdAt)
	if err != nil {
		if isUniqueViolation(err) {
			// The table's UNIQUE(project_id, name) constraint — surface
			// this as a clean 409, never the raw Postgres
			// constraint-violation message (this file's step 3
			// requirement).
			writeError(w, amerrors.New(amerrors.CodeAlreadyExists, "an mcp server named \""+req.Name+"\" is already registered for this project"), http.StatusConflict)
			return
		}
		writeStoreError(w, amerrors.Wrap(amerrors.CodeUnavailable, "inserting mcp server", err))
		return
	}

	if req.RuleDSL != "" {
		_, err = tx.Exec(ctx, `
			INSERT INTO guardrail_policies (project_id, mcp_server_id, rule_dsl, enabled)
			VALUES ($1, $2, $3, true)
		`, projectID.String(), serverID, req.RuleDSL)
		if err != nil {
			writeStoreError(w, amerrors.Wrap(amerrors.CodeUnavailable, "inserting guardrail policy", err))
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		writeStoreError(w, amerrors.Wrap(amerrors.CodeUnavailable, "committing tx", err))
		return
	}

	writeJSON(w, http.StatusCreated, MCPServerView{
		ID:           serverID,
		Name:         req.Name,
		UpstreamURL:  req.UpstreamURL,
		Transport:    req.Transport,
		Version:      req.Version,
		Owner:        req.Owner,
		ManifestYAML: req.ManifestYAML,
		CreatedAt:    createdAt.UTC().Format(time.RFC3339),
	})
}

// listServers returns every mcp_servers row scoped to projectID, the
// tenant boundary (Architecture.md §13) — never an unscoped query.
func (h *MCPRegistryHandler) listServers(w http.ResponseWriter, r *http.Request, projectID ids.ProjectID) {
	rows, err := h.pool.Query(r.Context(), `
		SELECT id, name, upstream_url, transport, version, owner, manifest_yaml, created_at
		FROM mcp_servers
		WHERE project_id = $1
		ORDER BY created_at DESC
	`, projectID.String())
	if err != nil {
		writeStoreError(w, amerrors.Wrap(amerrors.CodeUnavailable, "listing mcp servers", err))
		return
	}
	defer rows.Close()

	servers := make([]MCPServerView, 0)
	for rows.Next() {
		var v MCPServerView
		var createdAt time.Time
		if err := rows.Scan(&v.ID, &v.Name, &v.UpstreamURL, &v.Transport, &v.Version, &v.Owner, &v.ManifestYAML, &createdAt); err != nil {
			writeStoreError(w, amerrors.Wrap(amerrors.CodeInternal, "scanning mcp server row", err))
			return
		}
		v.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		servers = append(servers, v)
	}
	if err := rows.Err(); err != nil {
		writeStoreError(w, amerrors.Wrap(amerrors.CodeUnavailable, "iterating mcp servers", err))
		return
	}
	writeJSON(w, http.StatusOK, servers)
}

// getServer fetches one server scoped to (id, projectID). Scoping both
// columns in the WHERE clause — rather than fetching by id and checking
// project_id in Go — means a server belonging to another project produces
// exactly the same "not found" outcome as an id that does not exist at
// all: never a distinguishable "exists but forbidden" response (the
// GraphQL trace resolver's cross-tenant-safety pattern in
// internal/graphql/schema.go, applied here).
func (h *MCPRegistryHandler) getServer(w http.ResponseWriter, r *http.Request, projectID ids.ProjectID, serverIDStr string) {
	var v MCPServerView
	var createdAt time.Time
	err := h.pool.QueryRow(r.Context(), `
		SELECT id, name, upstream_url, transport, version, owner, manifest_yaml, created_at
		FROM mcp_servers
		WHERE id = $1 AND project_id = $2
	`, serverIDStr, projectID.String()).Scan(&v.ID, &v.Name, &v.UpstreamURL, &v.Transport, &v.Version, &v.Owner, &v.ManifestYAML, &createdAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, amerrors.New(amerrors.CodeNotFound, "mcp server not found"), http.StatusNotFound)
			return
		}
		writeStoreError(w, amerrors.Wrap(amerrors.CodeUnavailable, "querying mcp server", err))
		return
	}
	v.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	writeJSON(w, http.StatusOK, v)
}

// deleteServer removes one server scoped to (id, projectID); the row's
// FK-cascade (schema/postgres/004_mcp_registry.sql's `ON DELETE CASCADE`
// on guardrail_policies.mcp_server_id and mcp_server_tokens.mcp_server_id)
// takes care of its policy and token rows in the same statement.
func (h *MCPRegistryHandler) deleteServer(w http.ResponseWriter, r *http.Request, projectID ids.ProjectID, serverIDStr string) {
	tag, err := h.pool.Exec(r.Context(), `DELETE FROM mcp_servers WHERE id = $1 AND project_id = $2`, serverIDStr, projectID.String())
	if err != nil {
		writeStoreError(w, amerrors.Wrap(amerrors.CodeUnavailable, "deleting mcp server", err))
		return
	}
	if tag.RowsAffected() == 0 {
		// Same cross-tenant-safety stance as getServer: a foreign
		// project's server id is indistinguishable from one that never
		// existed.
		writeError(w, amerrors.New(amerrors.CodeNotFound, "mcp server not found"), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// createToken issues a new opaque bearer token for callers of one
// registered server — the OAuth 2.1-style caller-facing auth
// Architecture.md §13 requires the Gateway to enforce independently of
// AgentMesh's own API-key auth. Mirrors SetupHandler's api_keys
// generation exactly: crypto/rand + hex, just "mcp_" instead of
// "am_live_", and authkeys.Hash/Prefix instead of a locally reimplemented
// hash so the two token kinds can never silently diverge on what
// "hashed at rest" means.
//
// The raw token is returned in this response body and nowhere else —
// only its hash and display prefix are persisted (mirrors api_keys'
// "shown once" convention). A caller that loses it must issue a new one;
// there is no recovery/reveal endpoint by design.
func (h *MCPRegistryHandler) createToken(w http.ResponseWriter, r *http.Request, projectID ids.ProjectID, serverIDStr string) {
	var req createTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, amerrors.Wrap(amerrors.CodeInvalidArgument, "decoding request body", err), http.StatusBadRequest)
		return
	}
	if req.CallerName == "" {
		writeError(w, amerrors.New(amerrors.CodeInvalidArgument, "caller_name is required"), http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Verify the server belongs to the caller's project before issuing a
	// token scoped to it — same never-leak-cross-project-existence stance
	// as getServer/deleteServer.
	var exists bool
	if err := h.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mcp_servers WHERE id = $1 AND project_id = $2)`, serverIDStr, projectID.String()).Scan(&exists); err != nil {
		writeStoreError(w, amerrors.Wrap(amerrors.CodeUnavailable, "checking mcp server ownership", err))
		return
	}
	if !exists {
		writeError(w, amerrors.New(amerrors.CodeNotFound, "mcp server not found"), http.StatusNotFound)
		return
	}

	rawBytes := make([]byte, 16)
	if _, err := rand.Read(rawBytes); err != nil {
		writeStoreError(w, amerrors.Wrap(amerrors.CodeInternal, "generating token bytes", err))
		return
	}
	rawToken := "mcp_" + hex.EncodeToString(rawBytes)
	hashedToken := authkeys.Hash(rawToken)
	prefix, err := authkeys.Prefix(rawToken)
	if err != nil {
		writeStoreError(w, amerrors.Wrap(amerrors.CodeInternal, "computing token prefix", err))
		return
	}

	if _, err := h.pool.Exec(ctx, `
		INSERT INTO mcp_server_tokens (mcp_server_id, hashed_token, prefix, caller_name)
		VALUES ($1, $2, $3, $4)
	`, serverIDStr, hashedToken, prefix, req.CallerName); err != nil {
		writeStoreError(w, amerrors.Wrap(amerrors.CodeUnavailable, "inserting mcp server token", err))
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{
		"token":  rawToken,
		"prefix": prefix,
	})
}

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505) — used to translate the mcp_servers
// UNIQUE(project_id, name) constraint into a clean 409 instead of letting
// a raw driver error reach writeStoreError's generic 5xx mapping.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
