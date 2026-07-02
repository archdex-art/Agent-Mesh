// Package ws implements the Realtime Gateway's client-facing transport:
// upgrade an authenticated HTTP request to a WebSocket, subscribe it to
// one project's span-event stream via hub.Hub, and push events until the
// client disconnects. Communication Layer table in Architecture.md §9
// specifies "WebSocket (Realtime Gateway)" as the live-tailing transport.
package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/agentmesh/agentmesh/services/realtime-gateway/internal/hub"
	"github.com/agentmesh/agentmesh/shared/authkeys"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	// Realtime Gateway is deployed behind whatever reverse proxy fronts
	// the rest of AgentMesh's HTTP surface (same as Query API); origin
	// checking is deliberately permissive at MVP scale since the actual
	// access control is the API key, not same-origin — a stricter
	// CheckOrigin policy is a Milestone 8 hardening item, not blocking
	// here.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Handler upgrades authenticated requests to WebSocket connections and
// streams that caller's project span events.
type Handler struct {
	hub       *hub.Hub
	authStore authkeys.Store
	logger    *slog.Logger
}

func NewHandler(h *hub.Hub, authStore authkeys.Store, logger *slog.Logger) *Handler {
	return &Handler{hub: h, authStore: authStore, logger: logger}
}

// ServeHTTP implements the tail endpoint: GET /v1/tail?api_key=am_live_...
//
// The API key travels as a query parameter, not a header, because
// browsers' native WebSocket API cannot set custom headers on the
// upgrade request (the Web Console's live-tailing view needs this same
// endpoint, not just the CLI) — the tradeoff is documented here rather
// than silently deviating from the header-based auth every other
// AgentMesh HTTP surface uses (docs/otlp-mapping.md, Query API REST).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rawKey := r.URL.Query().Get("api_key")
	if rawKey == "" {
		http.Error(w, "missing api_key query parameter", http.StatusUnauthorized)
		return
	}

	record, err := authkeys.Authenticate(r.Context(), h.authStore, rawKey)
	if err != nil {
		http.Error(w, "invalid API key", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Warn("websocket upgrade failed", "err", err)
		return
	}
	defer conn.Close()

	events, unsubscribe := h.hub.Subscribe(record.ProjectID.String())
	defer unsubscribe()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Drain client-initiated messages on a goroutine purely to detect
	// disconnects promptly (this endpoint is server -> client push only;
	// AgentMesh never expects inbound tail-control messages at v0.1).
	go func() {
		defer cancel()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			payload, err := json.Marshal(event)
			if err != nil {
				continue
			}
			if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		}
	}
}
