// Package pubsub subscribes to the Collector's per-project Redis pub/sub
// channels (System Design.md §3's ingestion flow: "Coll->>Redis: publish
// span event (project_id channel)") and fans each message out to whatever
// local WebSocket sessions are currently subscribed to that project, via
// the hub package. This package owns exactly one responsibility: bridging
// Redis -> in-process fan-out; it never talks to a client connection
// directly (that's hub.Hub's job), keeping the two independently testable.
package pubsub

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/redis/go-redis/v9"
)

// SpanEvent is the wire shape published by the Collector on a span's
// project channel — a lightweight summary, not the full span payload,
// since the Realtime Gateway's job is "notify a live tail session a span
// arrived," not "replace the Query API." Fields mirror what `agentmesh
// tail` needs to render one line per span (Milestone 5's CLI use case).
type SpanEvent struct {
	TraceID    string `json:"trace_id"`
	SpanID     string `json:"span_id"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
}

// ChannelForProject returns the Redis pub/sub channel name a given
// project's span events are published on. Kept as a function (not just
// duplicated fmt.Sprintf calls) so the Collector's publisher and this
// subscriber can never drift on the naming convention.
func ChannelForProject(projectID string) string {
	return "agentmesh:spans:" + projectID
}

// Fanout is the dependency Subscriber hands decoded events to — satisfied
// by hub.Hub, declared here as a minimal interface so this package never
// imports hub directly (keeps the dependency direction one-way: cmd/
// wires pubsub -> hub, not pubsub depending on hub's concrete type).
type Fanout interface {
	Broadcast(projectID string, event SpanEvent)
}

// Subscriber owns a single Redis client and dispatches incoming messages
// to a Fanout.
type Subscriber struct {
	client *redis.Client
	fanout Fanout
	logger *slog.Logger
}

func New(client *redis.Client, fanout Fanout, logger *slog.Logger) *Subscriber {
	return &Subscriber{client: client, fanout: fanout, logger: logger}
}

// SubscribeProject subscribes to one project's channel and forwards
// decoded events to Fanout until ctx is canceled. Safe to call
// concurrently for distinct project IDs — each call owns its own Redis
// pub/sub connection (go-redis multiplexes these efficiently over a
// shared connection pool).
func (s *Subscriber) SubscribeProject(ctx context.Context, projectID string) error {
	pubsub := s.client.Subscribe(ctx, ChannelForProject(projectID))
	defer pubsub.Close()

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-ch:
			if !ok {
				return nil
			}
			var event SpanEvent
			if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
				s.logger.Warn("dropping malformed span event", "project_id", projectID, "err", err)
				continue
			}
			s.fanout.Broadcast(projectID, event)
		}
	}
}
