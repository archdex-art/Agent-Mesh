// Package registry implements the MCP Gateway's per-server routing
// lookup: given a caller's authenticated AgentMesh project and a server
// name parsed from the request path, resolve which upstream MCP server to
// proxy to (docs/plan/Architecture.md §13's "the Gateway routes to many
// registered servers, not one static upstream" requirement, Milestone 6 —
// "MCP Registry (Postgres schema + Query API/Console CRUD for server
// manifests)").
//
// This package's shape deliberately mirrors shared/authkeys' Store /
// PostgresStore pattern (a dependency-injected interface at the package
// boundary, Phase 3's "independently testable" standard) scoped to the
// mcp_servers / guardrail_policies tables from
// schema/postgres/004_mcp_registry.sql instead of api_keys.
//
// Both the Gateway (this package) and the Query API's registry REST
// surface read the same Postgres tables independently — exactly like the
// Collector and Query API already independently read the spans table —
// so this package never imports, and is never imported by, the Query
// API's registry code.
package registry

import (
	"context"

	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/google/uuid"
)

// Server is a registered MCP server's routing-relevant metadata — enough
// for the router to construct a per-request proxy.Gateway.
type Server struct {
	ID          uuid.UUID
	Name        string
	UpstreamURL string
	Transport   string // "stdio" or "streamable-http", per the mcp_servers CHECK constraint
}

// Store resolves registered servers and their guardrail policy documents
// by project. It is an interface (not a concrete struct) so the router's
// unit tests can substitute a fake without a live database.
type Store interface {
	// ResolveServer returns the Server row named name scoped to
	// projectID, or an amerrors.CodeNotFound error if no such server is
	// registered for that project. A server registered under a
	// *different* project must be indistinguishable from a nonexistent
	// one — the same cross-tenant safety property the Query API's
	// GraphQL trace resolver already establishes — so this method never
	// leaks whether the name exists elsewhere.
	ResolveServer(ctx context.Context, projectID ids.ProjectID, name string) (Server, error)

	// GuardrailPolicy returns the rule_dsl of serverID's currently
	// enabled guardrail_policies row, if one exists. ok is false (with a
	// nil error) when the server has no enabled policy row — a normal,
	// expected state for a server registered without one — letting the
	// router fall back to its static/empty default engine rather than
	// treating "no policy configured" as a failure.
	GuardrailPolicy(ctx context.Context, serverID uuid.UUID) (ruleDSL string, ok bool, err error)
}

// wrapRowError classifies a Postgres query error for either lookup
// method above: pgx's "no rows" sentinel becomes AgentMesh's typed
// CodeNotFound (a clean, expected outcome the router turns into a
// JSON-RPC error rather than a 500), anything else is a genuine
// connectivity/query failure wrapped as CodeUnavailable so callers can
// tell "doesn't exist" from "couldn't check" apart (Architecture.md §17's
// retryable-vs-terminal distinction).
func wrapRowError(err error, notFoundMessage, queryDescription string) error {
	if isNoRows(err) {
		return amerrors.New(amerrors.CodeNotFound, notFoundMessage)
	}
	return amerrors.Wrap(amerrors.CodeUnavailable, queryDescription, err)
}
