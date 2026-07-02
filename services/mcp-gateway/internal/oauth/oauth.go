// Package oauth implements the MCP Gateway's caller-facing bearer-token
// authentication: the "OAuth 2.1 as the caller-facing auth mechanism ...
// independent of AgentMesh's own API-key auth — a caller authenticates to
// the *tool*, not to AgentMesh" requirement from docs/plan/Architecture.md
// §13, backed by the mcp_server_tokens table in
// schema/postgres/004_mcp_registry.sql. A full OAuth 2.1
// authorization-code+PKCE flow is out of scope (per that migration's
// header comment: it doesn't fit MCP's machine-to-machine calling
// pattern); this package implements the operationally-relevant subset —
// opaque bearer tokens, hashed at rest, revocable.
//
// This package's shape deliberately mirrors shared/authkeys' Store /
// Authenticate pattern exactly — same hashing convention (authkeys.Hash,
// reused directly rather than reimplemented, so the two token families
// can never silently diverge on what "hashed at rest" means), same
// interface-at-the-boundary testability story (Phase 3's "independently
// testable" standard) — scoped to mcp_server_tokens instead of api_keys.
//
// Unlike authkeys, this package does not ship a CachedStore wrapper: M6
// accepted that every bearer-token check hits Postgres directly rather
// than ship an untested caching layer under schedule pressure. Adding one
// later is an additive wrapper around Store (exactly how authkeys.
// CachedStore wraps authkeys.Store), not a breaking change to this
// package's shape.
package oauth

import (
	"context"
	"errors"
	"strings"

	"github.com/agentmesh/agentmesh/shared/authkeys"
	amerrors "github.com/agentmesh/agentmesh/shared/errors"
)

// tokenPrefix is the opaque bearer token's required leading marker,
// matching the generation convention the Query API's server-token
// endpoint uses when minting new tokens ("mcp_" + random hex, mirroring
// rest.NewSetupHandler's existing "am_live_" + random hex API-key
// convention). A token missing this prefix is rejected before it ever
// reaches the hash function or Postgres.
const tokenPrefix = "mcp_"

// ErrMalformedToken is returned when a caller-supplied token does not
// start with tokenPrefix — distinct from "token not found," which is a
// well-shaped token that simply isn't registered (or was revoked).
var ErrMalformedToken = errors.New("oauth: malformed bearer token")

// TokenRecord is a validated bearer token's routing-relevant metadata.
type TokenRecord struct {
	MCPServerID string // mcp_server_tokens.mcp_server_id, as a canonical UUID string; the router must confirm this matches the server the caller is actually addressing
	CallerName  string // mcp_server_tokens.caller_name — the human-readable audit identity attributed to emitted mcp.call spans
}

// Store looks up hashed bearer tokens against Postgres. It is an
// interface (not a concrete struct) so the router's unit tests can
// substitute a fake without a live database.
type Store interface {
	// LookupByHash returns the TokenRecord for a non-revoked token whose
	// hash equals hashedToken, or an amerrors.CodeNotFound error if no
	// such token exists or it has been revoked.
	LookupByHash(ctx context.Context, hashedToken string) (TokenRecord, error)
}

// Authenticate validates a raw caller-supplied bearer token end to end:
// checks its shape, hashes it via authkeys.Hash (the same hashing
// convention as every other AgentMesh secret), and looks it up via
// store. It mirrors authkeys.Authenticate's exact structure and
// error-collapsing behavior — shape failures and lookup misses both
// surface as CodeUnauthenticated — so a caller cannot distinguish
// "malformed" from "revoked" from "never existed," an anti-enumeration
// property carried over from authkeys' precedent. The router turns any
// error this returns into the same JSON-RPC -32001 response regardless
// of which branch produced it.
func Authenticate(ctx context.Context, store Store, rawToken string) (TokenRecord, error) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return TokenRecord{}, amerrors.New(amerrors.CodeUnauthenticated, "missing bearer token")
	}
	if !strings.HasPrefix(rawToken, tokenPrefix) {
		return TokenRecord{}, amerrors.Wrap(amerrors.CodeUnauthenticated, "malformed bearer token", ErrMalformedToken)
	}

	record, err := store.LookupByHash(ctx, authkeys.Hash(rawToken))
	if err != nil {
		return TokenRecord{}, amerrors.Wrap(amerrors.CodeUnauthenticated, "invalid or revoked bearer token", err)
	}
	return record, nil
}
