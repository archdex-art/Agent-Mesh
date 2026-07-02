package registry

import (
	"context"
	"errors"

	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore is the production Store implementation, querying the
// mcp_servers and guardrail_policies tables defined in
// schema/postgres/004_mcp_registry.sql.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore returns a PostgresStore backed by pool. The pool's
// lifecycle is owned by the caller.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

// isNoRows reports whether err is pgx's "no matching row" sentinel,
// isolated to this file so registry.go's error-classification logic
// (wrapRowError) stays a plain, dependency-light function its own unit
// tests can exercise with a manufactured pgx.ErrNoRows value — no live
// connection required.
func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

// ResolveServer implements Store by querying mcp_servers on its
// (project_id, name) unique index — the same index that enforces at most
// one server per name per project at write time, so this lookup can
// never be ambiguous.
func (s *PostgresStore) ResolveServer(ctx context.Context, projectID ids.ProjectID, name string) (Server, error) {
	var idStr, upstreamURL, transport string
	err := s.pool.QueryRow(ctx,
		`SELECT id, upstream_url, transport FROM mcp_servers WHERE project_id = $1 AND name = $2`,
		projectID.String(), name,
	).Scan(&idStr, &upstreamURL, &transport)
	if err != nil {
		return Server{}, wrapRowError(err, "no mcp server registered with that name for this project", "querying mcp_servers")
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		return Server{}, amerrors.Wrap(amerrors.CodeInternal, "parsing mcp_servers.id", err)
	}

	return Server{ID: id, Name: name, UpstreamURL: upstreamURL, Transport: transport}, nil
}

// GuardrailPolicy implements Store by querying the unique partial index
// over enabled policy rows (idx_guardrail_policies_one_enabled_per_server),
// so there is at most one row to find per server.
func (s *PostgresStore) GuardrailPolicy(ctx context.Context, serverID uuid.UUID) (string, bool, error) {
	var ruleDSL string
	err := s.pool.QueryRow(ctx,
		`SELECT rule_dsl FROM guardrail_policies WHERE mcp_server_id = $1 AND enabled = true`,
		serverID.String(),
	).Scan(&ruleDSL)
	if err != nil {
		if isNoRows(err) {
			return "", false, nil
		}
		return "", false, amerrors.Wrap(amerrors.CodeUnavailable, "querying guardrail_policies", err)
	}
	return ruleDSL, true, nil
}
