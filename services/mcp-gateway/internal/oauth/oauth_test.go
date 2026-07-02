package oauth

import (
	"context"
	"testing"

	"github.com/agentmesh/agentmesh/shared/authkeys"
	amerrors "github.com/agentmesh/agentmesh/shared/errors"
)

// fakeStore mirrors shared/authkeys' authkeys_test.go fakeStore pattern
// exactly, scoped to TokenRecord instead of Record — no live Postgres
// required to exercise Authenticate's orchestration logic.
type fakeStore struct {
	calls   int
	byHash  map[string]TokenRecord
	failErr error
}

func (f *fakeStore) LookupByHash(ctx context.Context, hashedToken string) (TokenRecord, error) {
	f.calls++
	if f.failErr != nil {
		return TokenRecord{}, f.failErr
	}
	rec, ok := f.byHash[hashedToken]
	if !ok {
		return TokenRecord{}, amerrors.New(amerrors.CodeNotFound, "not found")
	}
	return rec, nil
}

func TestAuthenticateSucceeds(t *testing.T) {
	rawToken := "mcp_validsecret1234567890"
	store := &fakeStore{byHash: map[string]TokenRecord{
		authkeys.Hash(rawToken): {MCPServerID: "server-1", CallerName: "billing-agent"},
	}}

	rec, err := Authenticate(context.Background(), store, rawToken)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if rec.MCPServerID != "server-1" {
		t.Fatalf("MCPServerID = %q, want %q", rec.MCPServerID, "server-1")
	}
	if rec.CallerName != "billing-agent" {
		t.Fatalf("CallerName = %q, want %q", rec.CallerName, "billing-agent")
	}
}

func TestAuthenticateUsesAuthkeysHashConvention(t *testing.T) {
	// This pins the requirement that oauth reuses authkeys.Hash directly
	// rather than reimplementing SHA-256 hashing — a store seeded with a
	// record under authkeys.Hash(rawToken) must be the exact key
	// Authenticate looks up.
	rawToken := "mcp_anothersecret"
	store := &fakeStore{byHash: map[string]TokenRecord{
		authkeys.Hash(rawToken): {MCPServerID: "server-2", CallerName: "caller"},
	}}

	if _, err := Authenticate(context.Background(), store, rawToken); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
}

func TestAuthenticateRejectsEmptyToken(t *testing.T) {
	store := &fakeStore{byHash: map[string]TokenRecord{}}
	_, err := Authenticate(context.Background(), store, "")
	if amerrors.CodeOf(err) != amerrors.CodeUnauthenticated {
		t.Fatalf("CodeOf(err) = %v, want CodeUnauthenticated", amerrors.CodeOf(err))
	}
	if store.calls != 0 {
		t.Fatalf("store.LookupByHash called %d times for an empty token, want 0", store.calls)
	}
}

func TestAuthenticateRejectsMissingPrefix(t *testing.T) {
	store := &fakeStore{byHash: map[string]TokenRecord{}}
	_, err := Authenticate(context.Background(), store, "am_live_wrongfamily")
	if amerrors.CodeOf(err) != amerrors.CodeUnauthenticated {
		t.Fatalf("CodeOf(err) = %v, want CodeUnauthenticated", amerrors.CodeOf(err))
	}
	if store.calls != 0 {
		t.Fatalf("store.LookupByHash called %d times for a malformed token, want 0 (should fail fast)", store.calls)
	}
}

func TestAuthenticateRejectsUnknownToken(t *testing.T) {
	store := &fakeStore{byHash: map[string]TokenRecord{}}
	_, err := Authenticate(context.Background(), store, "mcp_unknownsecret1234")
	if amerrors.CodeOf(err) != amerrors.CodeUnauthenticated {
		t.Fatalf("CodeOf(err) = %v, want CodeUnauthenticated", amerrors.CodeOf(err))
	}
}

func TestAuthenticateRejectsRevokedOrStoreError(t *testing.T) {
	// A revoked token's PostgresStore.LookupByHash misses the partial
	// index and surfaces as CodeNotFound, exactly like "never existed" —
	// Authenticate must collapse both into CodeUnauthenticated so a
	// caller cannot distinguish the two (anti-enumeration, same as
	// authkeys.Authenticate's precedent).
	store := &fakeStore{failErr: amerrors.Wrap(amerrors.CodeUnavailable, "querying mcp_server_tokens", context.DeadlineExceeded)}
	_, err := Authenticate(context.Background(), store, "mcp_sometoken")
	if amerrors.CodeOf(err) != amerrors.CodeUnauthenticated {
		t.Fatalf("CodeOf(err) = %v, want CodeUnauthenticated", amerrors.CodeOf(err))
	}
}

func TestAuthenticateTrimsWhitespace(t *testing.T) {
	rawToken := "mcp_validsecret1234567890"
	store := &fakeStore{byHash: map[string]TokenRecord{
		authkeys.Hash(rawToken): {MCPServerID: "server-1", CallerName: "caller"},
	}}

	rec, err := Authenticate(context.Background(), store, "  "+rawToken+"  ")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if rec.MCPServerID != "server-1" {
		t.Fatalf("MCPServerID = %q, want %q", rec.MCPServerID, "server-1")
	}
}
