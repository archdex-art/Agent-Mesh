// Package authkeys implements AgentMesh's shared API-key hashing and
// lookup logic — the mechanism documented in Architecture.md §13's
// "Ingestion-path key validation" note.
//
// Both the Collector (ingestion) and the Query API (read access) validate
// caller-supplied API keys against the same Postgres `api_keys` table
// (schema/postgres/001_projects_and_api_keys.sql); this package is the
// single place that hashing algorithm and lookup query live, so the two
// services can never silently diverge on what "a valid key" means.
package authkeys

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
)

// ErrMalformedKey is returned when a caller-supplied key does not match
// AgentMesh's expected `am_<role>_<secret>` shape at all — distinct from
// "key not found," which is a valid key shape that simply isn't registered
// (or was revoked).
var ErrMalformedKey = errors.New("authkeys: malformed API key")

// Hash returns the hex-encoded SHA-256 digest of rawKey. Architecture.md §13
// requires keys to be "hashed at rest" — SHA-256 (not a slow KDF like
// bcrypt) is the deliberate choice here because API keys are high-entropy
//, machine-generated secrets (unlike user passwords), so the offline
// brute-force resistance a slow KDF buys is not needed, and the ingestion
// path validates a key on every span batch — a KDF's deliberate slowness
// would directly tax the hot path this document's caching strategy exists
// to protect.
func Hash(rawKey string) string {
	sum := sha256.Sum256([]byte(rawKey))
	return hex.EncodeToString(sum[:])
}

// Prefix extracts the displayable prefix from a raw key (e.g. "am_live_ab12"
// from "am_live_ab12cdef..."), matching the convention in
// schema/postgres/001_projects_and_api_keys.sql's `prefix` column: enough
// characters to help a human identify a key in a list without exposing the
// full secret. Returns ErrMalformedKey if rawKey is shorter than the
// prefix length.
const prefixLength = 12

func Prefix(rawKey string) (string, error) {
	if len(rawKey) < prefixLength {
		return "", ErrMalformedKey
	}
	return rawKey[:prefixLength], nil
}

// Role is the permission level an API key grants, matching the
// `role` CHECK constraint in schema/postgres/001_projects_and_api_keys.sql.
type Role string

const (
	RoleIngest Role = "ingest"
	RoleRead   Role = "read"
	RoleAdmin  Role = "admin"
)

// Record is a validated API key's control-plane metadata, returned by a
// successful Lookup.
type Record struct {
	ID        ids.ProjectID // the api_keys.id column, reusing the UUIDv7 ProjectID type since both are control-plane UUIDs
	ProjectID ids.ProjectID
	Role      Role
}

// Store looks up hashed API keys against Postgres. It is an interface
// (not a concrete struct) so the Collector and Query API can each
// construct it against their own pgxpool.Pool, and so unit tests can
// substitute a fake without a live database (Phase 3's "independently
// testable" standard).
type Store interface {
	// LookupByHash returns the Record for a non-revoked key whose hash
	// equals hashedKey, or an amerrors.CodeNotFound error if no such key
	// exists or it has been revoked.
	LookupByHash(ctx context.Context, hashedKey string) (Record, error)
}

// CachedStore wraps a Store with an in-process, short-TTL cache, per
// Architecture.md §13's documented tradeoff: "a revoked key can ingest for
// up to the cache TTL," favoring ingestion-path availability over instant
// revocation.
type CachedStore struct {
	inner Store
	ttl   time.Duration
	now   func() time.Time // overridable in tests

	entries map[string]cacheEntry
}

type cacheEntry struct {
	record    Record
	expiresAt time.Time
}

// NewCachedStore wraps inner with a cache using the given ttl. A ttl of
// zero disables caching (every lookup goes to inner), useful for tests that
// want to assert on inner's call count directly.
func NewCachedStore(inner Store, ttl time.Duration) *CachedStore {
	return &CachedStore{
		inner:   inner,
		ttl:     ttl,
		now:     time.Now,
		entries: make(map[string]cacheEntry),
	}
}

// LookupByHash implements Store, consulting the cache before falling
// through to inner.
func (c *CachedStore) LookupByHash(ctx context.Context, hashedKey string) (Record, error) {
	if c.ttl > 0 {
		if entry, ok := c.entries[hashedKey]; ok && c.now().Before(entry.expiresAt) {
			return entry.record, nil
		}
	}

	record, err := c.inner.LookupByHash(ctx, hashedKey)
	if err != nil {
		return Record{}, err
	}

	if c.ttl > 0 {
		c.entries[hashedKey] = cacheEntry{record: record, expiresAt: c.now().Add(c.ttl)}
	}
	return record, nil
}

// Authenticate validates a raw caller-supplied API key end to end: checks
// its shape, hashes it, and looks it up via store. It is the single
// entry point both the Collector's gRPC interceptor and the Query API's
// HTTP middleware call, so the two services can never diverge on what
// "authenticated" means.
func Authenticate(ctx context.Context, store Store, rawKey string) (Record, error) {
	rawKey = strings.TrimSpace(rawKey)
	if rawKey == "" {
		return Record{}, amerrors.New(amerrors.CodeUnauthenticated, "missing API key")
	}
	if _, err := Prefix(rawKey); err != nil {
		return Record{}, amerrors.Wrap(amerrors.CodeUnauthenticated, "malformed API key", err)
	}

	record, err := store.LookupByHash(ctx, Hash(rawKey))
	if err != nil {
		return Record{}, amerrors.Wrap(amerrors.CodeUnauthenticated, "invalid or revoked API key", err)
	}
	return record, nil
}
