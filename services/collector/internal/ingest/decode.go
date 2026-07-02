// Package ingest implements the Collector's OTLP receiving and decoding
// path: the gRPC service that accepts ExportTraceServiceRequest messages and
// translates them into shared/span.Span values per docs/otlp-mapping.md's
// wire contract.
//
// Architecture.md §2 assigns the Collector exactly one responsibility:
// "Receive OTLP spans, validate schema, deduplicate, write to ClickHouse,
// stream to Realtime Gateway, forward to Cost Engine and Anomaly Detector."
// This file covers the first two (receive, validate); writer.go (added
// alongside the ClickHouse writer) covers persistence.
package ingest

import (
	"encoding/hex"
	"fmt"
	"time"

	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/agentmesh/agentmesh/shared/span"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// CurrentSchemaVersion mirrors shared/span.CurrentSchemaVersion; the
// decoder rejects any incoming span whose `agentmesh.schema_version`
// attribute does not equal this value (Technical Roadmap.md §9's rolling
// upgrade safety net).
const CurrentSchemaVersion = span.CurrentSchemaVersion

// Decoder translates OTLP ResourceSpans into shared/span.Span values,
// enforcing the attribute contract in docs/otlp-mapping.md and the
// caller-authenticated ProjectID passed in by the gRPC handler.
type Decoder struct{}

// NewDecoder returns a ready-to-use Decoder. Decoder holds no state, so a
// single instance is safe for concurrent use across every request the
// Collector serves.
func NewDecoder() *Decoder { return &Decoder{} }

// DecodeResourceSpans converts every span found across resourceSpans into
// shared/span.Span values. authenticatedProjectID is the ProjectID resolved
// from the caller's API key (never trusted from the OTLP payload itself);
// DecodeSpan cross-checks it against the `agentmesh.project_id` attribute
// per docs/otlp-mapping.md, so a caller cannot claim a different project
// than its key authorizes.
//
// DecodeResourceSpans returns the successfully decoded spans plus a slice of
// per-span decode errors (index-correlated to the flattened input order) so
// the gRPC handler can implement OTLP's partial-success semantics
// (ExportTracePartialSuccess) instead of failing an entire batch for one bad
// span.
func (d *Decoder) DecodeResourceSpans(resourceSpans []*tracepb.ResourceSpans, authenticatedProjectID ids.ProjectID) ([]span.Span, []error) {
	var decoded []span.Span
	var errs []error

	for _, rs := range resourceSpans {
		for _, ss := range rs.GetScopeSpans() {
			for _, otlpSpan := range ss.GetSpans() {
				s, err := d.DecodeSpan(otlpSpan, authenticatedProjectID)
				if err != nil {
					errs = append(errs, err)
					continue
				}
				decoded = append(decoded, s)
			}
		}
	}
	return decoded, errs
}

// DecodeSpan converts a single OTLP span into a shared/span.Span, applying
// docs/otlp-mapping.md's field and attribute contract, then runs
// shared/span.Span.Validate before returning it — malformed data is
// rejected here, at the ingestion boundary, per Architecture.md §17's
// error-handling philosophy.
func (d *Decoder) DecodeSpan(otlpSpan *tracepb.Span, authenticatedProjectID ids.ProjectID) (span.Span, error) {
	attrs := attributeMap(otlpSpan.GetAttributes())

	schemaVersion, err := requireIntAttr(attrs, "agentmesh.schema_version")
	if err != nil {
		return span.Span{}, amerrors.Wrap(amerrors.CodeInvalidArgument, "decoding span", err)
	}
	if schemaVersion != CurrentSchemaVersion {
		return span.Span{}, amerrors.New(amerrors.CodeSchemaVersionMismatch,
			fmt.Sprintf("span schema_version %d not recognized by this collector (expects %d)", schemaVersion, CurrentSchemaVersion))
	}

	claimedProjectIDStr, ok := attrs["agentmesh.project_id"]
	if !ok {
		return span.Span{}, amerrors.New(amerrors.CodeInvalidArgument, "span missing required agentmesh.project_id attribute")
	}
	claimedProjectID, err := ids.ParseProjectID(claimedProjectIDStr.GetStringValue())
	if err != nil {
		return span.Span{}, amerrors.Wrap(amerrors.CodeInvalidArgument, "parsing agentmesh.project_id", err)
	}
	if claimedProjectID != authenticatedProjectID {
		return span.Span{}, amerrors.New(amerrors.CodePermissionDenied,
			"span's agentmesh.project_id does not match the authenticated API key's project")
	}

	kindStr, ok := attrs["agentmesh.span_kind"]
	if !ok {
		return span.Span{}, amerrors.New(amerrors.CodeInvalidArgument, "span missing required agentmesh.span_kind attribute")
	}

	traceID, err := decodeTraceID(otlpSpan.GetTraceId())
	if err != nil {
		return span.Span{}, amerrors.Wrap(amerrors.CodeInvalidArgument, "decoding trace_id", err)
	}
	spanID, err := decodeSpanID(otlpSpan.GetSpanId())
	if err != nil {
		return span.Span{}, amerrors.Wrap(amerrors.CodeInvalidArgument, "decoding span_id", err)
	}

	var parentSpanID ids.SpanID
	if len(otlpSpan.GetParentSpanId()) > 0 {
		parentSpanID, err = decodeSpanID(otlpSpan.GetParentSpanId())
		if err != nil {
			return span.Span{}, amerrors.Wrap(amerrors.CodeInvalidArgument, "decoding parent_span_id", err)
		}
	}

	s := span.Span{
		SchemaVersion: schemaVersion,
		ProjectID:     authenticatedProjectID,
		TraceID:       traceID,
		SpanID:        spanID,
		ParentSpanID:  parentSpanID,
		Kind:          span.Kind(kindStr.GetStringValue()),
		Name:          otlpSpan.GetName(),
		StartTime:     time.Unix(0, int64(otlpSpan.GetStartTimeUnixNano())).UTC(),
		Attributes:    make(map[string]string),
	}

	if end := otlpSpan.GetEndTimeUnixNano(); end != 0 {
		s.EndTime = time.Unix(0, int64(end)).UTC()
	}

	if statusVal, ok := attrs["agentmesh.status"]; ok {
		s.Status = span.Status(statusVal.GetStringValue())
	}

	s.Input = decodePayload(attrs, "agentmesh.input.inline", "agentmesh.input.blob_ref")
	s.Output = decodePayload(attrs, "agentmesh.output.inline", "agentmesh.output.blob_ref")
	if s.Input.BlobRef != "" {
		return span.Span{}, amerrors.New(amerrors.CodeInvalidArgument,
			"agentmesh.input.blob_ref must not be set by an exporter; the Collector assigns blob_ref on ingestion (docs/otlp-mapping.md)")
	}
	if s.Output.BlobRef != "" {
		return span.Span{}, amerrors.New(amerrors.CodeInvalidArgument,
			"agentmesh.output.blob_ref must not be set by an exporter; the Collector assigns blob_ref on ingestion (docs/otlp-mapping.md)")
	}

	if v, ok := attrs["agentmesh.token.input"]; ok {
		n := uint32(v.GetIntValue())
		s.TokenInput = &n
	}
	if v, ok := attrs["agentmesh.token.output"]; ok {
		n := uint32(v.GetIntValue())
		s.TokenOutput = &n
	}
	if v, ok := attrs["agentmesh.cost_usd"]; ok {
		cost := v.GetDoubleValue()
		s.CostUSD = &cost
	}
	if v, ok := attrs["chaos.injected"]; ok {
		s.ChaosInjected = v.GetStringValue() == "true"
	}
	if v, ok := attrs["chaos.fault_type"]; ok {
		s.ChaosFaultType = v.GetStringValue()
	}

	// Every non-agentmesh.* attribute passes through verbatim as free-form
	// metadata (docs/otlp-mapping.md's "Any other attribute key" clause).
	// This includes chaos.injected/chaos.fault_type themselves (deliberately
	// unprefixed, per docs/otlp-mapping.md's chaos section) — they land in
	// both the dedicated ChaosInjected/ChaosFaultType fields above *and* the
	// generic Attributes map, so a caller reading raw attributes sees them
	// too, matching the "any other key passed through verbatim" contract
	// exactly rather than special-casing an exception to it.
	for k, v := range attrs {
		if len(k) >= len("agentmesh.") && k[:len("agentmesh.")] == "agentmesh." {
			continue
		}
		s.Attributes[k] = attrValueToString(v)
	}

	if err := s.Validate(); err != nil {
		return span.Span{}, amerrors.Wrap(amerrors.CodeInvalidArgument, "validating decoded span", err)
	}
	return s, nil
}

func attributeMap(kvs []*commonpb.KeyValue) map[string]*commonpb.AnyValue {
	m := make(map[string]*commonpb.AnyValue, len(kvs))
	for _, kv := range kvs {
		m[kv.GetKey()] = kv.GetValue()
	}
	return m
}

func requireIntAttr(attrs map[string]*commonpb.AnyValue, key string) (int, error) {
	v, ok := attrs[key]
	if !ok {
		return 0, fmt.Errorf("missing required attribute %q", key)
	}
	return int(v.GetIntValue()), nil
}

func decodePayload(attrs map[string]*commonpb.AnyValue, inlineKey, blobRefKey string) span.Payload {
	var p span.Payload
	if v, ok := attrs[inlineKey]; ok {
		p.Inline = v.GetStringValue()
	}
	if v, ok := attrs[blobRefKey]; ok {
		p.BlobRef = v.GetStringValue()
	}
	return p
}

func attrValueToString(v *commonpb.AnyValue) string {
	switch val := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return val.StringValue
	case *commonpb.AnyValue_BoolValue:
		if val.BoolValue {
			return "true"
		}
		return "false"
	case *commonpb.AnyValue_IntValue:
		return fmt.Sprintf("%d", val.IntValue)
	case *commonpb.AnyValue_DoubleValue:
		return fmt.Sprintf("%g", val.DoubleValue)
	default:
		return v.String()
	}
}

func decodeTraceID(raw []byte) (ids.TraceID, error) {
	if len(raw) != 16 {
		return ids.TraceID{}, fmt.Errorf("trace_id must be 16 bytes, got %d", len(raw))
	}
	return ids.ParseTraceID(hex.EncodeToString(raw))
}

func decodeSpanID(raw []byte) (ids.SpanID, error) {
	if len(raw) != 8 {
		return ids.SpanID{}, fmt.Errorf("span_id must be 8 bytes, got %d", len(raw))
	}
	return ids.ParseSpanID(hex.EncodeToString(raw))
}
