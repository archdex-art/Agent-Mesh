package authkeys

import (
	"context"
	"errors"
	"testing"
	"time"

	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
)

type fakeStore struct {
	calls   int
	byHash  map[string]Record
	failErr error
}

func (f *fakeStore) LookupByHash(ctx context.Context, hashedKey string) (Record, error) {
	f.calls++
	if f.failErr != nil {
		return Record{}, f.failErr
	}
	rec, ok := f.byHash[hashedKey]
	if !ok {
		return Record{}, amerrors.New(amerrors.CodeNotFound, "not found")
	}
	return rec, nil
}

func mustProjectID(t *testing.T) ids.ProjectID {
	t.Helper()
	id, err := ids.NewProjectID()
	if err != nil {
		t.Fatalf("NewProjectID: %v", err)
	}
	return id
}

func TestHashIsDeterministic(t *testing.T) {
	a := Hash("am_live_secret123")
	b := Hash("am_live_secret123")
	if a != b {
		t.Fatalf("Hash is not deterministic: %q != %q", a, b)
	}
}

func TestHashDiffersForDifferentKeys(t *testing.T) {
	a := Hash("am_live_secret123")
	b := Hash("am_live_secret456")
	if a == b {
		t.Fatal("different keys hashed to the same value")
	}
}

func TestPrefixExtractsLeadingChars(t *testing.T) {
	prefix, err := Prefix("am_live_ab12cdef34567890")
	if err != nil {
		t.Fatalf("Prefix: %v", err)
	}
	if prefix != "am_live_ab12" {
		t.Fatalf("Prefix = %q, want %q", prefix, "am_live_ab12")
	}
}

func TestPrefixRejectsShortKey(t *testing.T) {
	_, err := Prefix("short")
	if !errors.Is(err, ErrMalformedKey) {
		t.Fatalf("Prefix error = %v, want ErrMalformedKey", err)
	}
}

func TestAuthenticateSucceeds(t *testing.T) {
	rawKey := "am_live_validsecret1234567890"
	projectID := mustProjectID(t)
	keyID := mustProjectID(t)
	store := &fakeStore{byHash: map[string]Record{
		Hash(rawKey): {ID: keyID, ProjectID: projectID, Role: RoleIngest},
	}}

	rec, err := Authenticate(context.Background(), store, rawKey)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if rec.ProjectID != projectID {
		t.Fatalf("ProjectID = %v, want %v", rec.ProjectID, projectID)
	}
	if rec.Role != RoleIngest {
		t.Fatalf("Role = %v, want %v", rec.Role, RoleIngest)
	}
}

func TestAuthenticateRejectsEmptyKey(t *testing.T) {
	store := &fakeStore{byHash: map[string]Record{}}
	_, err := Authenticate(context.Background(), store, "")
	if amerrors.CodeOf(err) != amerrors.CodeUnauthenticated {
		t.Fatalf("CodeOf(err) = %v, want CodeUnauthenticated", amerrors.CodeOf(err))
	}
}

func TestAuthenticateRejectsMalformedKey(t *testing.T) {
	store := &fakeStore{byHash: map[string]Record{}}
	_, err := Authenticate(context.Background(), store, "short")
	if amerrors.CodeOf(err) != amerrors.CodeUnauthenticated {
		t.Fatalf("CodeOf(err) = %v, want CodeUnauthenticated", amerrors.CodeOf(err))
	}
	if store.calls != 0 {
		t.Fatalf("store.LookupByHash called %d times for a malformed key, want 0 (should fail fast)", store.calls)
	}
}

func TestAuthenticateRejectsUnknownKey(t *testing.T) {
	store := &fakeStore{byHash: map[string]Record{}}
	_, err := Authenticate(context.Background(), store, "am_live_unknownsecret1234")
	if amerrors.CodeOf(err) != amerrors.CodeUnauthenticated {
		t.Fatalf("CodeOf(err) = %v, want CodeUnauthenticated", amerrors.CodeOf(err))
	}
}

func TestAuthenticateTrimsWhitespace(t *testing.T) {
	rawKey := "am_live_validsecret1234567890"
	projectID := mustProjectID(t)
	store := &fakeStore{byHash: map[string]Record{
		Hash(rawKey): {ProjectID: projectID, Role: RoleIngest},
	}}

	rec, err := Authenticate(context.Background(), store, "  "+rawKey+"  ")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if rec.ProjectID != projectID {
		t.Fatalf("ProjectID = %v, want %v", rec.ProjectID, projectID)
	}
}

func TestCachedStoreServesFromCacheWithinTTL(t *testing.T) {
	projectID := mustProjectID(t)
	inner := &fakeStore{byHash: map[string]Record{
		"hash1": {ProjectID: projectID, Role: RoleIngest},
	}}
	fakeNow := time.Now()
	cached := NewCachedStore(inner, time.Minute)
	cached.now = func() time.Time { return fakeNow }

	if _, err := cached.LookupByHash(context.Background(), "hash1"); err != nil {
		t.Fatalf("first LookupByHash: %v", err)
	}
	if _, err := cached.LookupByHash(context.Background(), "hash1"); err != nil {
		t.Fatalf("second LookupByHash: %v", err)
	}
	if inner.calls != 1 {
		t.Fatalf("inner.calls = %d, want 1 (second lookup should be served from cache)", inner.calls)
	}
}

func TestCachedStoreExpiresAfterTTL(t *testing.T) {
	projectID := mustProjectID(t)
	inner := &fakeStore{byHash: map[string]Record{
		"hash1": {ProjectID: projectID, Role: RoleIngest},
	}}
	fakeNow := time.Now()
	cached := NewCachedStore(inner, time.Minute)
	cached.now = func() time.Time { return fakeNow }

	if _, err := cached.LookupByHash(context.Background(), "hash1"); err != nil {
		t.Fatalf("first LookupByHash: %v", err)
	}
	fakeNow = fakeNow.Add(2 * time.Minute) // advance past TTL
	if _, err := cached.LookupByHash(context.Background(), "hash1"); err != nil {
		t.Fatalf("second LookupByHash: %v", err)
	}
	if inner.calls != 2 {
		t.Fatalf("inner.calls = %d, want 2 (cache should have expired)", inner.calls)
	}
}

func TestCachedStoreDoesNotCacheErrors(t *testing.T) {
	inner := &fakeStore{byHash: map[string]Record{}}
	cached := NewCachedStore(inner, time.Minute)

	if _, err := cached.LookupByHash(context.Background(), "missing"); err == nil {
		t.Fatal("expected error for missing key")
	}
	if _, err := cached.LookupByHash(context.Background(), "missing"); err == nil {
		t.Fatal("expected error for missing key (second call)")
	}
	if inner.calls != 2 {
		t.Fatalf("inner.calls = %d, want 2 (errors must not be cached)", inner.calls)
	}
}

func TestCachedStoreZeroTTLDisablesCaching(t *testing.T) {
	projectID := mustProjectID(t)
	inner := &fakeStore{byHash: map[string]Record{
		"hash1": {ProjectID: projectID, Role: RoleIngest},
	}}
	cached := NewCachedStore(inner, 0)

	cached.LookupByHash(context.Background(), "hash1")
	cached.LookupByHash(context.Background(), "hash1")
	if inner.calls != 2 {
		t.Fatalf("inner.calls = %d, want 2 (ttl=0 should disable caching)", inner.calls)
	}
}

func TestAuthenticateRevokedKeyPropagatesStoreError(t *testing.T) {
	store := &fakeStore{failErr: amerrors.New(amerrors.CodeUnavailable, "db down")}
	_, err := Authenticate(context.Background(), store, "am_live_validsecret1234567890")
	if amerrors.CodeOf(err) != amerrors.CodeUnauthenticated {
		// Authenticate wraps any store failure as Unauthenticated for the
		// caller (never leaking "db down" as a distinct signal an attacker
		// could use to distinguish "wrong key" from "server trouble").
		t.Fatalf("CodeOf(err) = %v, want CodeUnauthenticated", amerrors.CodeOf(err))
	}
}
