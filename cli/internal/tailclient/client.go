// Package tailclient connects to the Realtime Gateway's WebSocket
// endpoint and decodes incoming span events for the TUI to render
// (Architecture.md §10: "agentmesh tail --project <id> — live TUI ...
// streaming spans as they arrive via the Realtime Gateway").
package tailclient

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/gorilla/websocket"
)

// SpanEvent mirrors realtime-gateway's pubsub.SpanEvent wire shape.
// Duplicated (not imported) deliberately: the CLI is a separately
// distributed binary with no compile-time dependency on any server-side
// module, consistent with docs/otlp-mapping.md's "wire contract, not
// shared Go types" boundary between AgentMesh's independently
// deployable/distributed artifacts.
type SpanEvent struct {
	TraceID    string `json:"trace_id"`
	SpanID     string `json:"span_id"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
}

// Client streams SpanEvents from a Realtime Gateway over WebSocket.
type Client struct {
	conn *websocket.Conn
}

// Dial connects to gatewayURL's /v1/tail endpoint, authenticating via
// the api_key query parameter (matching the Gateway's documented
// browser-compatibility tradeoff in
// services/realtime-gateway/internal/ws/handler.go).
func Dial(gatewayURL, apiKey string) (*Client, error) {
	u, err := url.Parse(gatewayURL)
	if err != nil {
		return nil, fmt.Errorf("parsing gateway URL: %w", err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
		// already correct
	default:
		return nil, fmt.Errorf("unsupported gateway URL scheme %q (want http/https/ws/wss)", u.Scheme)
	}
	u.Path = "/v1/tail"
	q := u.Query()
	q.Set("api_key", apiKey)
	u.RawQuery = q.Encode()

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("connecting to realtime gateway: %w", err)
	}
	return &Client{conn: conn}, nil
}

// Close closes the underlying WebSocket connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// ReadEvent blocks until the next SpanEvent arrives or the connection
// closes/errors.
func (c *Client) ReadEvent() (SpanEvent, error) {
	_, payload, err := c.conn.ReadMessage()
	if err != nil {
		return SpanEvent{}, err
	}
	var event SpanEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return SpanEvent{}, fmt.Errorf("decoding span event: %w", err)
	}
	return event, nil
}
