package authkeys

import (
	"context"

	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore is the production Store implementation, querying the
// `api_keys` table defined in schema/postgres/001_projects_and_api_keys.sql.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore returns a PostgresStore backed by pool. The pool's
// lifecycle is owned by the caller.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

// LookupByHash implements Store by querying the partial index over
// non-revoked keys (schema/postgres/001_projects_and_api_keys.sql's
// idx_api_keys_active_hashed_key), so a revoked key correctly misses.
func (s *PostgresStore) LookupByHash(ctx context.Context, hashedKey string) (Record, error) {
	var idStr, projectIDStr, role string
	err := s.pool.QueryRow(ctx,
		`SELECT id, project_id, role FROM api_keys WHERE hashed_key = $1 AND revoked_at IS NULL`,
		hashedKey,
	).Scan(&idStr, &projectIDStr, &role)

	if err != nil {
		if err == pgx.ErrNoRows {
			return Record{}, amerrors.New(amerrors.CodeNotFound, "no active api key matches the given hash")
		}
		return Record{}, amerrors.Wrap(amerrors.CodeUnavailable, "querying api_keys", err)
	}

	id, err := ids.ParseProjectID(idStr)
	if err != nil {
		return Record{}, amerrors.Wrap(amerrors.CodeInternal, "parsing api_keys.id", err)
	}
	projectID, err := ids.ParseProjectID(projectIDStr)
	if err != nil {
		return Record{}, amerrors.Wrap(amerrors.CodeInternal, "parsing api_keys.project_id", err)
	}

	return Record{ID: id, ProjectID: projectID, Role: Role(role)}, nil
}
