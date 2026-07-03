// project_provisioning.go holds the transactional "create a project plus
// its first API key" operation shared by every entry point that
// provisions a new tenant: the anonymous POST /v1/setup (setup.go,
// ownerUserID == nil, unauthenticated, for self-hosted/CI/local-dev use)
// and the authenticated POST /v1/auth/projects (auth.go, ownerUserID ==
// the caller's user id). Extracted into one helper so the two can never
// silently diverge on what "provisioning a project" means — the same
// rationale documented for authkeys.Authenticate being the single entry
// point both the Collector and Query API call for API-key validation.
package rest

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/agentmesh/agentmesh/shared/authkeys"
	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/jackc/pgx/v5"
)

// createProjectAndKey inserts a new projects row (owned by *ownerUserID,
// or anonymous if ownerUserID is nil — schema/postgres/006_users.sql
// made projects.owner_user_id nullable specifically so both callers can
// share this one insert) and its first api_keys row, both in tx, so a
// caller can never observe a project that exists without a usable key
// or vice versa — the exact transactional guarantee setup.go's original
// inline version already provided, preserved here unchanged.
//
// name is used verbatim if non-empty (POST /v1/auth/projects lets an
// authenticated caller choose their own project name); an empty name
// falls back to "Project <8 hex chars>", matching setup.go's original
// naming convention. Those 8 chars are deliberately taken from the
// TAIL of the id, not the head as setup.go's original inline version
// did: a UUIDv7's first 8 hex chars sit entirely inside its 48-bit
// millisecond timestamp (ids.go's newUUIDv7), so they are IDENTICAL
// for any two ids minted within the same ~65-second window (2^16 ms) —
// which collided against projects.name's UNIQUE constraint under
// back-to-back anonymous project creation (surfaced by this file's own
// integration tests). The last 8 hex chars fall entirely within the
// id's random tail (RFC 9562 rand_b), so they vary per call regardless
// of timing, while the visible "Project <8 hex>" format — and
// /v1/setup's response, which never echoes name anyway — is unchanged.
// The resolved name is returned alongside the id (a deliberate addition
// beyond a minimal "just the id and key" helper) because POST
// /v1/auth/projects' response contract includes "name", and only this
// function knows the post-default value.
func createProjectAndKey(ctx context.Context, tx pgx.Tx, ownerUserID *string, name string) (projectID, projectName, rawKey string, err error) {
	pid, err := ids.NewProjectID()
	if err != nil {
		return "", "", "", amerrors.Wrap(amerrors.CodeInternal, "generating project id", err)
	}
	projectID = pid.String()

	projectName = name
	if projectName == "" {
		projectName = "Project " + projectID[len(projectID)-8:]
	}

	rawBytes := make([]byte, 16)
	if _, err := rand.Read(rawBytes); err != nil {
		return "", "", "", amerrors.Wrap(amerrors.CodeInternal, "generating key bytes", err)
	}
	rawKey = "am_live_" + hex.EncodeToString(rawBytes)

	// authkeys.Hash/Prefix rather than a locally reimplemented SHA-256 +
	// slice, so this and mcp_registry.go's createToken can never
	// silently diverge on what "hashed at rest" or "the display prefix"
	// means (mcp_registry.go's createToken doc comment states the same
	// rationale).
	hashedKey := authkeys.Hash(rawKey)
	prefix, err := authkeys.Prefix(rawKey)
	if err != nil {
		return "", "", "", amerrors.Wrap(amerrors.CodeInternal, "computing key prefix", err)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO projects (id, name, owner_user_id) VALUES ($1, $2, $3)`,
		projectID, projectName, ownerUserID,
	); err != nil {
		return "", "", "", amerrors.Wrap(amerrors.CodeUnavailable, "inserting project", err)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO api_keys (id, project_id, hashed_key, prefix, role) VALUES (gen_random_uuid(), $1, $2, $3, 'ingest')`,
		projectID, hashedKey, prefix,
	); err != nil {
		return "", "", "", amerrors.Wrap(amerrors.CodeUnavailable, "inserting api key", err)
	}

	return projectID, projectName, rawKey, nil
}

// rotateProjectKey mints a fresh API key for an already-provisioned
// project and revokes every previously active key, in one transaction —
// the recovery path for AuthGate's ProjectPicker: an existing project's
// raw key is shown once at creation and never re-exposed (see
// userProjectView's doc comment), so re-selecting an existing project
// from the picker has no key to fall back to. Rotating instead of just
// minting an additional key keeps "how many active keys can a project
// have" at the same invariant createProjectAndKey establishes (exactly
// one), so callers never have to reason about which of several active
// keys is "the" one currently in use.
func rotateProjectKey(ctx context.Context, tx pgx.Tx, projectID string) (rawKey string, err error) {
	rawBytes := make([]byte, 16)
	if _, err := rand.Read(rawBytes); err != nil {
		return "", amerrors.Wrap(amerrors.CodeInternal, "generating key bytes", err)
	}
	rawKey = "am_live_" + hex.EncodeToString(rawBytes)

	hashedKey := authkeys.Hash(rawKey)
	prefix, err := authkeys.Prefix(rawKey)
	if err != nil {
		return "", amerrors.Wrap(amerrors.CodeInternal, "computing key prefix", err)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE api_keys SET revoked_at = now() WHERE project_id = $1 AND revoked_at IS NULL`,
		projectID,
	); err != nil {
		return "", amerrors.Wrap(amerrors.CodeUnavailable, "revoking previous api keys", err)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO api_keys (id, project_id, hashed_key, prefix, role) VALUES (gen_random_uuid(), $1, $2, $3, 'ingest')`,
		projectID, hashedKey, prefix,
	); err != nil {
		return "", amerrors.Wrap(amerrors.CodeUnavailable, "inserting api key", err)
	}

	return rawKey, nil
}
