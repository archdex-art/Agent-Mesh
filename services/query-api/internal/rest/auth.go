// auth.go implements the Web Console's real-account REST surface:
// register, login, "who am I", and list/create-project scoped to the
// caller's own account (schema/postgres/006_users.sql). This is a NEW,
// ADDITIONAL auth layer on top of the existing anonymous POST /v1/setup
// path (setup.go, kept working unmodified for self-hosted/CI/local-dev
// use) — session tokens minted by login authenticate ONLY these
// account-management endpoints; every existing project-data endpoint
// (traces.go, mcp_registry.go, internal/graphql/schema.go) keeps
// authenticating exclusively via the X-AgentMesh-API-Key header through
// authz.Middleware/ProjectIDFromRequest, completely untouched.
package rest

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/agentmesh/agentmesh/services/query-api/internal/authz"
	"github.com/agentmesh/agentmesh/shared/authkeys"
	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// emailPattern is a basic, not-exhaustive-RFC-5322 sanity check ("looks
// like an email") — enough to reject obvious typos at signup time
// without attempting to fully validate an address, which can only
// really be done by sending mail to it (this endpoint does not).
var emailPattern = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// minPasswordLength matches this file's documented contract.
const minPasswordLength = 8

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// createUserProjectRequest is POST /v1/auth/projects' request body. Name
// is optional — an empty value falls back to createProjectAndKey's
// setup.go-derived default naming convention.
type createUserProjectRequest struct {
	Name string `json:"name"`
}

// userProjectView is one row of GET /v1/auth/projects' response: enough
// for the Console's project switcher to list "which projects do I own"
// and show which key each is using, without a full key-list endpoint —
// APIKeyPrefix is a pointer because a project could in principle have no
// active (non-revoked) key left, even though every project created
// through this file or setup.go starts with exactly one.
type userProjectView struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	CreatedAt    string  `json:"created_at"`
	APIKeyPrefix *string `json:"api_key_prefix"`
}

// AuthHandler serves the account-management REST surface. Like
// SetupHandler and MCPRegistryHandler, it talks to Postgres directly via
// *pgxpool.Pool — no reader interface, matching mcp_registry.go's
// documented precedent for a handler this size that owns transactional
// writes.
type AuthHandler struct {
	pool *pgxpool.Pool
}

// NewAuthHandler returns a handler backed by pool.
func NewAuthHandler(pool *pgxpool.Pool) *AuthHandler {
	return &AuthHandler{pool: pool}
}

// ServeHTTP dispatches on path + method. Unlike MCPRegistryHandler,
// there is no single upfront auth check here: register and login are
// deliberately reachable with no credentials at all (a caller cannot
// present a session token to obtain one), while me and both
// GET/POST projects require authz.SessionMiddleware to have already run.
// cmd/main.go wires this same *AuthHandler under two different mux
// registrations — one bare, one wrapped in authz.SessionMiddleware —
// exactly like setup.go (unauthenticated) and traces.go/mcp_registry.go
// (wrapped in authz.Middleware) already coexist today.
//
//	POST /v1/auth/register  -> register       (no auth)
//	POST /v1/auth/login     -> login          (no auth)
//	GET  /v1/auth/me        -> me             (session auth)
//	GET  /v1/auth/projects  -> listProjects   (session auth)
//	POST /v1/auth/projects  -> createProject  (session auth)
func (h *AuthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v1/auth/register":
		if r.Method != http.MethodPost {
			writeError(w, amerrors.New(amerrors.CodeInvalidArgument, "method not allowed"), http.StatusMethodNotAllowed)
			return
		}
		h.register(w, r)
	case "/v1/auth/login":
		if r.Method != http.MethodPost {
			writeError(w, amerrors.New(amerrors.CodeInvalidArgument, "method not allowed"), http.StatusMethodNotAllowed)
			return
		}
		h.login(w, r)
	case "/v1/auth/me":
		if r.Method != http.MethodGet {
			writeError(w, amerrors.New(amerrors.CodeInvalidArgument, "method not allowed"), http.StatusMethodNotAllowed)
			return
		}
		h.me(w, r)
	case "/v1/auth/projects":
		switch r.Method {
		case http.MethodGet:
			h.listProjects(w, r)
		case http.MethodPost:
			h.createProject(w, r)
		default:
			writeError(w, amerrors.New(amerrors.CodeInvalidArgument, "method not allowed"), http.StatusMethodNotAllowed)
		}
	default:
		if strings.HasSuffix(r.URL.Path, "/rotate-key") && strings.HasPrefix(r.URL.Path, "/v1/auth/projects/") {
			if r.Method != http.MethodPost {
				writeError(w, amerrors.New(amerrors.CodeInvalidArgument, "method not allowed"), http.StatusMethodNotAllowed)
				return
			}
			projectID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/auth/projects/"), "/rotate-key")
			h.rotateProjectKey(w, r, projectID)
			return
		}
		writeError(w, amerrors.New(amerrors.CodeNotFound, "not found"), http.StatusNotFound)
	}
}

// register creates a new user account: bcrypt-hashes the password
// (bcrypt.DefaultCost — schema/postgres/006_users.sql's header comment
// explains why this differs from authkeys.Hash's SHA-256: a password is
// a low-entropy human secret needing a slow KDF's brute-force
// resistance, unlike a machine-generated, high-entropy API key) and
// inserts into users. The raw password is never logged or echoed back.
func (h *AuthHandler) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, amerrors.Wrap(amerrors.CodeInvalidArgument, "decoding request body", err), http.StatusBadRequest)
		return
	}
	if !emailPattern.MatchString(req.Email) {
		writeError(w, amerrors.New(amerrors.CodeInvalidArgument, "email must look like a valid email address"), http.StatusBadRequest)
		return
	}
	if len(req.Password) < minPasswordLength {
		writeError(w, amerrors.New(amerrors.CodeInvalidArgument, "password must be at least 8 characters"), http.StatusBadRequest)
		return
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeStoreError(w, amerrors.Wrap(amerrors.CodeInternal, "hashing password", err))
		return
	}

	var userID string
	err = h.pool.QueryRow(r.Context(),
		`INSERT INTO users (email, hashed_password) VALUES ($1, $2) RETURNING id`,
		req.Email, string(hashed),
	).Scan(&userID)
	if err != nil {
		if isUniqueViolation(err) {
			// The table's UNIQUE(email) constraint — surface a clean
			// 409, never the raw Postgres constraint-violation message
			// (same stance as mcp_registry.go's createServer).
			writeError(w, amerrors.New(amerrors.CodeAlreadyExists, "an account with this email already exists"), http.StatusConflict)
			return
		}
		writeStoreError(w, amerrors.Wrap(amerrors.CodeUnavailable, "inserting user", err))
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"user_id": userID})
}

// login verifies the caller's email/password against the stored bcrypt
// hash and, on success, issues a new opaque session token. Wrong
// password and unknown email deliberately produce the exact same 401 —
// distinguishing them would let a caller enumerate registered emails.
func (h *AuthHandler) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, amerrors.Wrap(amerrors.CodeInvalidArgument, "decoding request body", err), http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	var userID, storedHash string
	err := h.pool.QueryRow(ctx, `SELECT id, hashed_password FROM users WHERE email = $1`, req.Email).Scan(&userID, &storedHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeInvalidCredentials(w)
			return
		}
		writeStoreError(w, amerrors.Wrap(amerrors.CodeUnavailable, "querying user", err))
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(req.Password)); err != nil {
		writeInvalidCredentials(w)
		return
	}

	// crypto/rand + hex, "ams_" prefixed ("AgentMesh Session") — the
	// same generation convention as every other opaque token in this
	// codebase (setup.go's api_keys "am_live_" prefix, mcp_registry.go's
	// createToken "mcp_" prefix), just a new prefix so a session token
	// is never mistaken for either of those at a glance.
	rawBytes := make([]byte, 16)
	if _, err := rand.Read(rawBytes); err != nil {
		writeStoreError(w, amerrors.Wrap(amerrors.CodeInternal, "generating session token bytes", err))
		return
	}
	rawToken := "ams_" + hex.EncodeToString(rawBytes)

	// SHA-256 (authkeys.Hash) is fine for the session token itself — it
	// is a high-entropy, machine-generated secret exactly like an API
	// key, unlike the user's password above (this file's contract).
	tokenHash := authkeys.Hash(rawToken)

	if _, err := h.pool.Exec(ctx,
		`INSERT INTO sessions (user_id, token_hash, expires_at) VALUES ($1, $2, now() + interval '30 days')`,
		userID, tokenHash,
	); err != nil {
		writeStoreError(w, amerrors.Wrap(amerrors.CodeUnavailable, "inserting session", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"session_token": rawToken,
		"user_id":       userID,
	})
}

// me returns the identity SessionMiddleware resolved for the caller's
// bearer token.
func (h *AuthHandler) me(w http.ResponseWriter, r *http.Request) {
	userID, email, err := authz.UserFromContext(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"user_id": userID, "email": email})
}

// listProjects returns every project owned by the caller, plus each
// project's first still-active API key prefix (never the full key —
// raw keys are shown once, at creation, and never re-exposed) so the
// Console can show "which key is this project using" without a
// separate full key-list endpoint.
func (h *AuthHandler) listProjects(w http.ResponseWriter, r *http.Request) {
	userID, _, err := authz.UserFromContext(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}

	rows, err := h.pool.Query(r.Context(), `
		SELECT p.id, p.name, p.created_at, active_key.prefix
		FROM projects p
		LEFT JOIN LATERAL (
			SELECT prefix FROM api_keys
			WHERE project_id = p.id AND revoked_at IS NULL
			ORDER BY created_at ASC
			LIMIT 1
		) active_key ON true
		WHERE p.owner_user_id = $1
		ORDER BY p.created_at DESC
	`, userID)
	if err != nil {
		writeStoreError(w, amerrors.Wrap(amerrors.CodeUnavailable, "listing projects", err))
		return
	}
	defer rows.Close()

	projects := make([]userProjectView, 0)
	for rows.Next() {
		var v userProjectView
		var createdAt time.Time
		if err := rows.Scan(&v.ID, &v.Name, &createdAt, &v.APIKeyPrefix); err != nil {
			writeStoreError(w, amerrors.Wrap(amerrors.CodeInternal, "scanning project row", err))
			return
		}
		v.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		projects = append(projects, v)
	}
	if err := rows.Err(); err != nil {
		writeStoreError(w, amerrors.Wrap(amerrors.CodeUnavailable, "iterating projects", err))
		return
	}
	writeJSON(w, http.StatusOK, projects)
}

// createProject provisions a new project owned by the caller plus its
// first API key, in one transaction — createProjectAndKey
// (project_provisioning.go) is the exact same insert logic setup.go's
// anonymous path uses, just with ownerUserID set instead of nil.
func (h *AuthHandler) createProject(w http.ResponseWriter, r *http.Request) {
	userID, _, err := authz.UserFromContext(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}

	// Body is optional (name defaults when omitted): a bare POST with no
	// body must not be rejected as a decode error, so io.EOF (empty
	// body) is treated the same as "no name supplied".
	var req createUserProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, amerrors.Wrap(amerrors.CodeInvalidArgument, "decoding request body", err), http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		writeStoreError(w, amerrors.Wrap(amerrors.CodeUnavailable, "starting tx", err))
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once Commit has succeeded; the return value has no recovery action either way

	projectID, projectName, rawKey, err := createProjectAndKey(ctx, tx, &userID, req.Name)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		writeStoreError(w, amerrors.Wrap(amerrors.CodeUnavailable, "committing tx", err))
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{
		"project_id": projectID,
		"name":       projectName,
		"api_key":    rawKey,
	})
}

// rotateProjectKey mints a fresh, single active API key for a project
// the caller owns — the ProjectPicker's "recover access" action for an
// existing project whose original key (shown once at creation) was
// lost. Ownership is verified against projects.owner_user_id before
// any mutation so one account can never mint keys for another
// account's project by guessing its id.
func (h *AuthHandler) rotateProjectKey(w http.ResponseWriter, r *http.Request, projectID string) {
	userID, _, err := authz.UserFromContext(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}

	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		writeStoreError(w, amerrors.Wrap(amerrors.CodeUnavailable, "starting tx", err))
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once Commit has succeeded; the return value has no recovery action either way

	var ownerUserID *string
	if err := tx.QueryRow(ctx, `SELECT owner_user_id FROM projects WHERE id = $1`, projectID).Scan(&ownerUserID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, amerrors.New(amerrors.CodeNotFound, "project not found"), http.StatusNotFound)
			return
		}
		writeStoreError(w, amerrors.Wrap(amerrors.CodeUnavailable, "looking up project", err))
		return
	}
	if ownerUserID == nil || *ownerUserID != userID {
		writeError(w, amerrors.New(amerrors.CodeNotFound, "project not found"), http.StatusNotFound)
		return
	}

	rawKey, err := rotateProjectKey(ctx, tx, projectID)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		writeStoreError(w, amerrors.Wrap(amerrors.CodeUnavailable, "committing tx", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"project_id": projectID,
		"api_key":    rawKey,
	})
}

func writeInvalidCredentials(w http.ResponseWriter) {
	writeError(w, amerrors.New(amerrors.CodeUnauthenticated, "invalid email or password"), http.StatusUnauthorized)
}
