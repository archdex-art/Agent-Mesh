// Package hub implements the in-process fan-out from a Redis-subscribed
// span event to every live WebSocket session watching that project.
// Architecture.md §2 assigns the Realtime Gateway exactly one
// responsibility: "Push live span updates to connected Web
// Console/CLI sessions as a trace is still in flight" — Hub is that
// responsibility's entire implementation, independent of the transport
// (ws package) and the source (pubsub package) on either side of it.
package hub

import (
	"context"
	"log/slog"
	"sync"

	"github.com/agentmesh/agentmesh/services/realtime-gateway/internal/pubsub"
)

// Subscriber is what SubscribeProject needs to start listening on Redis
// for a project's channel — satisfied by *pubsub.Subscriber, declared
// here (not imported as a concrete type) so Hub stays testable against a
// fake without a live Redis connection.
type Subscriber interface {
	SubscribeProject(ctx context.Context, projectID string) error
}

// client is one connected WebSocket session's outbound event queue.
// Buffered so a slow client (temporary network hiccup) does not block
// Broadcast for every other client sharing the project — Broadcast drops
// events for a client whose buffer is full rather than blocking the
// whole hub on one laggard connection.
type client struct {
	events chan pubsub.SpanEvent
}

// Hub tracks, per project_id, which local WebSocket sessions are
// currently subscribed and lazily starts/stops the underlying Redis
// subscription so the Gateway never pays for a Redis subscription no
// client is listening to.
type Hub struct {
	mu         sync.Mutex
	subscriber Subscriber
	logger     *slog.Logger

	// projects maps project_id -> the set of locally-connected clients
	// plus the cancel func for that project's Redis subscription
	// goroutine (nil until the first client subscribes).
	projects map[string]*projectState
}

type projectState struct {
	clients map[*client]struct{}
	cancel  context.CancelFunc
}

// New returns a Hub with no Subscriber attached yet. Callers construct a
// Hub before their pubsub.Subscriber (Hub implements pubsub.Fanout, so
// the Subscriber needs the Hub to exist first) and wire the Subscriber
// back in via AttachSubscriber once both are built — see cmd/main.go for
// the exact two-phase construction this breaks the cycle with.
func New(logger *slog.Logger) *Hub {
	return &Hub{
		logger:   logger,
		projects: make(map[string]*projectState),
	}
}

// AttachSubscriber sets the Subscriber a Hub uses to start per-project
// Redis subscriptions. Must be called exactly once, before the first
// call to Subscribe.
func (h *Hub) AttachSubscriber(subscriber Subscriber) {
	h.subscriber = subscriber
}

// Broadcast implements pubsub.Fanout: deliver event to every locally
// connected client currently subscribed to projectID.
func (h *Hub) Broadcast(projectID string, event pubsub.SpanEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()

	state, ok := h.projects[projectID]
	if !ok {
		return
	}
	for c := range state.clients {
		select {
		case c.events <- event:
		default:
			// Buffer full: drop rather than block, per the client
			// struct's documented tradeoff above.
			h.logger.Warn("dropping span event for slow client", "project_id", projectID)
		}
	}
}

// Subscribe registers a new local client for projectID, starting the
// underlying Redis subscription on the first subscriber for that
// project, and returns the channel the caller (the ws package's
// connection handler) should read events from plus an unsubscribe func
// that must be called exactly once when the connection closes.
func (h *Hub) Subscribe(projectID string) (events <-chan pubsub.SpanEvent, unsubscribe func()) {
	h.mu.Lock()
	defer h.mu.Unlock()

	state, ok := h.projects[projectID]
	if !ok {
		ctx, cancel := context.WithCancel(context.Background())
		state = &projectState{clients: make(map[*client]struct{}), cancel: cancel}
		h.projects[projectID] = state
		go func() {
			if err := h.subscriber.SubscribeProject(ctx, projectID); err != nil && ctx.Err() == nil {
				h.logger.Error("redis subscription ended unexpectedly", "project_id", projectID, "err", err)
			}
		}()
	}

	c := &client{events: make(chan pubsub.SpanEvent, 64)}
	state.clients[c] = struct{}{}

	unsubscribe = func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		st, ok := h.projects[projectID]
		if !ok {
			return
		}
		delete(st.clients, c)
		close(c.events)
		if len(st.clients) == 0 {
			// Last local listener gone: stop paying for the Redis
			// subscription until someone subscribes again.
			st.cancel()
			delete(h.projects, projectID)
		}
	}
	return c.events, unsubscribe
}
