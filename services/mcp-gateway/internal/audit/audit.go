// Package audit implements the MCP Gateway's OTLP audit-span emitter: for
// every "tools/call" request the Gateway sees (allowed or denied), it emits
// an `mcp.call` span to the Collector, per Architecture.md §3's
// KindMCPCall definition ("a tool call routed through the MCP Gateway,
// captured automatically even if the calling agent has no SDK
// integration") and docs/plan/MCP_Gateway_Architecture.md's sequence
// diagram step "GW->>Coll: Emit mcp.call span".
//
// This package builds OTLP spans by hand (not via the OpenTelemetry Go
// SDK) because the wire shape it must produce is docs/otlp-mapping.md's
// exact `agentmesh.*` attribute contract — the same contract the Python
// SDK's exporter.py and the Collector's decode.go already implement.
// Reusing that contract (rather than the OTel SDK's own span-building
// API, which has no notion of AgentMesh's attributes) keeps all three
// independent implementations honest against one shared document.
package audit

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/agentmesh/agentmesh/shared/span"
	collectorpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// apiKeyMetadataKey mirrors services/collector/internal/ingest/server.go's
// apiKeyMetadataKey exactly — the Gateway authenticates to the Collector
// the same way the Python SDK does.
const apiKeyMetadataKey = "x-agentmesh-api-key"

// Call is the data the Gateway has on hand when it wants to audit a
// tools/call request: enough to construct one mcp.call span.
type Call struct {
	ProjectID    ids.ProjectID
	ToolName     string
	Status       span.Status // StatusOK or StatusDenied
	DenyReason   string      // non-empty only when Status == StatusDenied
	StartTime    time.Time
	EndTime      time.Time
	CallerRecord string // free-form caller identity for the audit trail (e.g. api key prefix)
}

// Emitter sends Call records to the Collector as mcp.call OTLP spans.
type Emitter struct {
	conn   *grpc.ClientConn
	client collectorpb.TraceServiceClient
	apiKey string
}

// NewEmitter dials the Collector at endpoint (e.g. "localhost:4317") and
// returns a ready-to-use Emitter. apiKey is the Gateway's own AgentMesh
// API key, used to authenticate its audit-span exports the same way any
// other OTLP exporter does.
func NewEmitter(endpoint, apiKey string) (*Emitter, error) {
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("audit: dialing collector at %s: %w", endpoint, err)
	}
	return &Emitter{
		conn:   conn,
		client: collectorpb.NewTraceServiceClient(conn),
		apiKey: apiKey,
	}, nil
}

// Close releases the underlying gRPC connection.
func (e *Emitter) Close() error {
	return e.conn.Close()
}

// Emit sends one mcp.call span for call. It is fire-and-forget from the
// proxy's perspective: audit-emission failures are logged by the caller,
// never allowed to affect whether the proxied MCP request itself succeeds
// or fails (Architecture.md §17's ingestion-path availability philosophy
// — losing an audit span is recoverable, breaking the proxied call is
// not).
func (e *Emitter) Emit(ctx context.Context, call Call) error {
	traceID, err := ids.NewTraceID()
	if err != nil {
		return fmt.Errorf("audit: generating trace id: %w", err)
	}
	spanID, err := ids.NewSpanID()
	if err != nil {
		return fmt.Errorf("audit: generating span id: %w", err)
	}

	traceIDBytes, err := hex.DecodeString(traceID.String())
	if err != nil {
		return fmt.Errorf("audit: decoding trace id hex: %w", err)
	}
	spanIDBytes, err := hex.DecodeString(spanID.String())
	if err != nil {
		return fmt.Errorf("audit: decoding span id hex: %w", err)
	}

	attrs := []*commonpb.KeyValue{
		intAttr("agentmesh.schema_version", int64(span.CurrentSchemaVersion)),
		strAttr("agentmesh.project_id", call.ProjectID.String()),
		strAttr("agentmesh.span_kind", string(span.KindMCPCall)),
		strAttr("agentmesh.status", string(call.Status)),
	}
	// deny_reason is deliberately unprefixed (not "agentmesh.deny_reason"):
	// docs/otlp-mapping.md's decoder contract silently drops any
	// unrecognized agentmesh.*-prefixed attribute rather than passing it
	// through — verified against the real Collector, which is exactly how
	// this was caught before it shipped. An unprefixed key rides the
	// existing "any other attribute key... passed through verbatim" path
	// the contract already documents for free-form metadata.
	if call.Status == span.StatusDenied && call.DenyReason != "" {
		attrs = append(attrs, strAttr("deny_reason", call.DenyReason))
	}
	if call.CallerRecord != "" {
		attrs = append(attrs, strAttr("caller", call.CallerRecord))
	}

	otlpSpan := &tracepb.Span{
		TraceId:           traceIDBytes,
		SpanId:            spanIDBytes,
		Name:              call.ToolName,
		StartTimeUnixNano: uint64(call.StartTime.UnixNano()),
		EndTimeUnixNano:   uint64(call.EndTime.UnixNano()),
		Attributes:        attrs,
	}

	req := &collectorpb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				ScopeSpans: []*tracepb.ScopeSpans{
					{Spans: []*tracepb.Span{otlpSpan}},
				},
			},
		},
	}

	outCtx := metadata.AppendToOutgoingContext(ctx, apiKeyMetadataKey, e.apiKey)
	if _, err := e.client.Export(outCtx, req); err != nil {
		return fmt.Errorf("audit: exporting mcp.call span: %w", err)
	}
	return nil
}

func strAttr(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}}}
}

func intAttr(key string, value int64) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: value}}}
}
