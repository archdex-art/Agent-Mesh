// Package ratelimit implements the MCP Gateway's per-(server, caller)
// request throttling: a Redis-backed fixed-window counter gating calls
// against the optional rate_limit.requests_per_minute value an operator
// may set in a server's guardrail policy document
// (services/mcp-gateway/internal/policy.Document's additive Milestone 6
// extension, docs/plan/Architecture.md §13).
//
// Fixed-window (INCR + EXPIRE only on the window's first increment) is a
// deliberate simplicity choice over a sliding-window or token-bucket
// algorithm: it lets a caller burst up to ~2x their configured rate
// across a window boundary in the worst case, which M6 accepts as
// "good enough" anti-abuse protection — this is a coarse guardrail
// against a runaway or misbehaving caller, not a billing-grade quota
// enforcement mechanism.
package ratelimit

import (
	"context"
	"time"

	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/redis/go-redis/v9"
)

// Limiter enforces a fixed-window request cap per key against Redis.
type Limiter struct {
	client *redis.Client
}

// New returns a Limiter backed by client. The client's lifecycle
// (including closing its connection pool) is owned by the caller.
func New(client *redis.Client) *Limiter {
	return &Limiter{client: client}
}

// Allow reports whether one more request under key is permitted within
// the current fixed window: it increments key's counter and, only when
// that increment produced the window's first count (count == 1), sets
// key's TTL to window. Every subsequent increment inside the same window
// reuses the TTL the first request set, so the window boundary is
// anchored to the first request in it rather than being pushed back by
// every later one (the "fixed" in "fixed-window" — a sliding window
// would re-arm the TTL on every hit).
//
// The router calls Allow with key = "<mcp_server_id>:<caller_name>", so
// the limit is scoped per (server, caller-token) pair, not globally or
// per-project.
func (l *Limiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	count, err := l.client.Incr(ctx, key).Result()
	if err != nil {
		return false, amerrors.Wrap(amerrors.CodeUnavailable, "incrementing rate limit counter", err)
	}
	if count == 1 {
		if err := l.client.Expire(ctx, key, window).Err(); err != nil {
			return false, amerrors.Wrap(amerrors.CodeUnavailable, "setting rate limit window ttl", err)
		}
	}
	return count <= int64(limit), nil
}
