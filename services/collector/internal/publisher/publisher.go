// Package publisher implements the Collector's half of the realtime
// fan-out path: after a span is durably written to ClickHouse, publish a
// lightweight event to the project's Redis channel so the Realtime
// Gateway can push it to any live-tailing session (System Design.md §3's
// ingestion flow: "Coll->>Redis: publish span event (project_id
// channel)"). Publishing is deliberately best-effort — a Redis outage
// degrades to "no live tail," never to "ingestion fails," matching
// Architecture.md §17's ingestion-path philosophy ("the Collector never
// blocks or crashes the customer's agent process").
package publisher

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/agentmesh/agentmesh/shared/span"
	"github.com/redis/go-redis/v9"
)

// event mirrors realtime-gateway's pubsub.SpanEvent wire shape exactly.
// Duplicated rather than shared as a Go type because the two services
// intentionally have no compile-time dependency on each other (only a
// documented wire contract) — the same boundary docs/otlp-mapping.md
// draws between the SDK and the Collector.
type event struct {
	TraceID    string `json:"trace_id"`
	SpanID     string `json:"span_id"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
}

// channelForProject must match realtime-gateway's
// pubsub.ChannelForProject naming exactly, or published events are
// silently never delivered to any subscriber.
func channelForProject(projectID string) string {
	return "agentmesh:spans:" + projectID
}

// Publisher publishes decoded spans to Redis for realtime fan-out.
type Publisher struct {
	client *redis.Client
	logger *slog.Logger
}

func New(client *redis.Client, logger *slog.Logger) *Publisher {
	return &Publisher{client: client, logger: logger}
}

// PublishBatch publishes one event per span in spans, best-effort: a
// publish failure is logged and skipped, never returned as an error,
// since a dropped live-tail notification is recoverable (the span is
// already durably in ClickHouse) but propagating this failure up would
// incorrectly fail an otherwise-successful ingestion request.
func (p *Publisher) PublishBatch(ctx context.Context, spans []span.Span) {
	if p == nil || p.client == nil {
		return
	}
	for _, s := range spans {
		e := event{
			TraceID:    s.TraceID.String(),
			SpanID:     s.SpanID.String(),
			Kind:       string(s.Kind),
			Name:       s.Name,
			Status:     string(s.Status),
			DurationMS: s.EndTime.Sub(s.StartTime).Milliseconds(),
		}

		payload, err := json.Marshal(e)
		if err != nil {
			p.logger.Warn("failed to marshal span event for publish", "err", err)
			continue
		}
		if err := p.client.Publish(ctx, channelForProject(s.ProjectID.String()), payload).Err(); err != nil {
			p.logger.Warn("failed to publish span event", "err", err)
		}
	}
}
