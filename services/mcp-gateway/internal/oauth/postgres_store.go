package oauth

import (
	"context"

	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore is the production Store implementation, querying the
// mcp_server_tokens table defined in schema/postgres/004_mcp_registry.sql.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore returns a PostgresStore backed by pool. The pool's
// lifecycle is owned by the caller.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

// LookupByHash implements Store by querying the partial index over
// non-revoked tokens (idx_mcp_server_tokens_active_hashed_token), so a
// revoked token correctly misses.
func (s *PostgresStore) LookupByHash(ctx context.Context, hashedToken string) (TokenRecord, error) {
	var mcpServerID, callerName string
	err := s.pool.QueryRow(ctx,
		`SELECT mcp_server_id, caller_name FROM mcp_server_tokens WHERE hashed_token = $1 AND revoked_at IS NULL`,
		hashedToken,
	).Scan(&mcpServerID, &callerName)

	if err != nil {
		if err == pgx.ErrNoRows {
			return TokenRecord{}, amerrors.New(amerrors.CodeNotFound, "no active mcp server token matches the given hash")
		}
		return TokenRecord{}, amerrors.Wrap(amerrors.CodeUnavailable, "querying mcp_server_tokens", err)
	}

	return TokenRecord{MCPServerID: mcpServerID, CallerName: callerName}, nil
}
