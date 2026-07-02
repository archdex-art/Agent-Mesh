package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newTestLimiter spins up an in-memory miniredis server (a real Redis
// wire-protocol implementation, not a hand-rolled mock) so Allow's actual
// INCR/EXPIRE/compare sequence is exercised against real Redis semantics
// — fixed-window boundary behavior is exactly the kind of logic that
// looks right and isn't, so this deliberately does not mock the backing
// store away entirely.
func newTestLimiter(t *testing.T) (*Limiter, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { client.Close() })
	return New(client), mr
}

func TestAllowPermitsRequestsUnderLimit(t *testing.T) {
	limiter, _ := newTestLimiter(t)
	ctx := context.Background()

	for i := range 3 {
		allowed, err := limiter.Allow(ctx, "server-a:caller-1", 3, time.Minute)
		if err != nil {
			t.Fatalf("Allow (request %d): %v", i+1, err)
		}
		if !allowed {
			t.Fatalf("Allow (request %d) = false, want true (limit is 3)", i+1)
		}
	}
}

func TestAllowDeniesRequestsOverLimit(t *testing.T) {
	limiter, _ := newTestLimiter(t)
	ctx := context.Background()

	for i := range 3 {
		if _, err := limiter.Allow(ctx, "server-a:caller-1", 3, time.Minute); err != nil {
			t.Fatalf("Allow (request %d): %v", i+1, err)
		}
	}

	allowed, err := limiter.Allow(ctx, "server-a:caller-1", 3, time.Minute)
	if err != nil {
		t.Fatalf("Allow (4th request): %v", err)
	}
	if allowed {
		t.Fatal("Allow (4th request) = true, want false (limit is 3, this is the 4th)")
	}
}

func TestAllowScopesCountersPerKey(t *testing.T) {
	limiter, _ := newTestLimiter(t)
	ctx := context.Background()

	// Exhaust caller-1's budget entirely.
	for i := 0; i < 2; i++ {
		if _, err := limiter.Allow(ctx, "server-a:caller-1", 2, time.Minute); err != nil {
			t.Fatalf("Allow (caller-1, request %d): %v", i+1, err)
		}
	}
	if allowed, _ := limiter.Allow(ctx, "server-a:caller-1", 2, time.Minute); allowed {
		t.Fatal("caller-1 should be over its own limit")
	}

	// A different caller against the same server must have its own
	// independent counter (rate limiting is per (server, caller), not
	// global or per-server-only).
	allowed, err := limiter.Allow(ctx, "server-a:caller-2", 2, time.Minute)
	if err != nil {
		t.Fatalf("Allow (caller-2): %v", err)
	}
	if !allowed {
		t.Fatal("caller-2's independent counter was incorrectly exhausted by caller-1's requests")
	}
}

func TestAllowResetsAfterWindowExpires(t *testing.T) {
	limiter, mr := newTestLimiter(t)
	ctx := context.Background()
	const key = "server-a:caller-1"

	if allowed, err := limiter.Allow(ctx, key, 1, time.Minute); err != nil || !allowed {
		t.Fatalf("Allow (1st request) = %v, %v; want true, nil", allowed, err)
	}
	if allowed, err := limiter.Allow(ctx, key, 1, time.Minute); err != nil || allowed {
		t.Fatalf("Allow (2nd request, same window) = %v, %v; want false, nil", allowed, err)
	}

	// Advance miniredis's clock past the window's TTL — this exercises
	// the actual EXPIRE call Allow issued on the first increment, not a
	// simulated reset, so a bug that forgot to set (or reset) the TTL
	// would leave the key alive and this assertion would fail.
	mr.FastForward(time.Minute + time.Second)

	allowed, err := limiter.Allow(ctx, key, 1, time.Minute)
	if err != nil {
		t.Fatalf("Allow (after window expiry): %v", err)
	}
	if !allowed {
		t.Fatal("Allow (after window expiry) = false, want true (a new window should have started)")
	}
}

func TestAllowSurfacesRedisUnavailableAsError(t *testing.T) {
	_, mr := newTestLimiter(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	limiter := New(client)

	// Simulate Redis becoming unreachable mid-request.
	client.Close()

	_, err := limiter.Allow(context.Background(), "server-a:caller-1", 5, time.Minute)
	if err == nil {
		t.Fatal("Allow() succeeded against a closed Redis connection, want an error")
	}
}
