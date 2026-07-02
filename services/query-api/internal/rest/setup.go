package rest

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
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

	// Generate a new Project ID
	projectID, err := ids.NewProjectID()
	if err != nil {
		writeStoreError(w, amerrors.Wrap(amerrors.CodeInternal, "generating project id", err))
		return
	}

	// Generate a new API Key
	rawBytes := make([]byte, 16)
	if _, err := rand.Read(rawBytes); err != nil {
		writeStoreError(w, amerrors.Wrap(amerrors.CodeInternal, "generating key bytes", err))
		return
	}
	rawKey := "am_live_" + hex.EncodeToString(rawBytes)

	hash := sha256.Sum256([]byte(rawKey))
	hashedKey := hex.EncodeToString(hash[:])
	prefix := rawKey[:12]

	// Insert into Postgres transactionally
	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		writeStoreError(w, amerrors.Wrap(amerrors.CodeUnavailable, "starting tx", err))
		return
	}
	defer tx.Rollback(ctx)

	// We use a unique project name. If "Default Project" exists, we append the ID.
	projectName := "Project " + projectID.String()[:8]

	_, err = tx.Exec(ctx, `INSERT INTO projects (id, name) VALUES ($1, $2)`, projectID.String(), projectName)
	if err != nil {
		writeStoreError(w, amerrors.Wrap(amerrors.CodeUnavailable, "inserting project", err))
		return
	}

	_, err = tx.Exec(ctx, `INSERT INTO api_keys (id, project_id, hashed_key, prefix, role) VALUES (gen_random_uuid(), $1, $2, $3, 'ingest')`, projectID.String(), hashedKey, prefix)
	if err != nil {
		writeStoreError(w, amerrors.Wrap(amerrors.CodeUnavailable, "inserting api key", err))
		return
	}

	if err := tx.Commit(ctx); err != nil {
		writeStoreError(w, amerrors.Wrap(amerrors.CodeUnavailable, "committing tx", err))
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{
		"project_id": projectID.String(),
		"api_key":    rawKey,
	})
}
