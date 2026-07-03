package rest

import (
	"net/http"

	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SetupHandler struct {
	pool *pgxpool.Pool
}

func NewSetupHandler(pool *pgxpool.Pool) *SetupHandler {
	return &SetupHandler{pool: pool}
}

func (h *SetupHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, amerrors.New(amerrors.CodeInvalidArgument, "method not allowed"), http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		writeStoreError(w, amerrors.Wrap(amerrors.CodeUnavailable, "starting tx", err))
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once Commit has succeeded; the return value has no recovery action either way

	// Anonymous: no owning user. schema/postgres/006_users.sql made
	// projects.owner_user_id nullable specifically so this endpoint
	// keeps working, completely unmodified from the caller's
	// perspective, for self-hosted/CI/local-dev use — see
	// project_provisioning.go for the shared project+key insert logic
	// this and POST /v1/auth/projects (auth.go) both call.
	projectID, _, rawKey, err := createProjectAndKey(ctx, tx, nil, "")
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
		"api_key":    rawKey,
	})
}
