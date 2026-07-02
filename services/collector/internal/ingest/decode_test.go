package ingest

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/agentmesh/agentmesh/shared/span"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

func strAttr(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}}}
}

func intAttr(key string, value int64) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: value}}}
}

func doubleAttr(key string, value float64) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: value}}}
}

func mustProjectID(t *testing.T) ids.ProjectID {
	t.Helper()
	id, err := ids.NewProjectID()
	if err != nil {
		t.Fatalf("NewProjectID: %v", err)
	}
	return id
}

// wellFormedSpan builds a syntactically valid OTLP span with the required
// agentmesh.* attributes, for the given projectID, mutable via opts.
func wellFormedSpan(t *testing.T, projectID ids.ProjectID, opts ...func(*tracepb.Span)) (*tracepb.Span, ids.TraceID, ids.SpanID) {
	t.Helper()
	traceID, err := ids.NewTraceID()
	if err != nil {
		t.Fatalf("NewTraceID: %v", err)
	}
	spanID, err := ids.NewSpanID()
	if err != nil {
		t.Fatalf("NewSpanID: %v", err)
	}
	traceIDBytes, err := hex.DecodeString(traceID.String())
	if err != nil {
		t.Fatalf("decoding trace id hex: %v", err)
	}
	spanIDBytes, err := hex.DecodeString(spanID.String())
	if err != nil {
		t.Fatalf("decoding span id hex: %v", err)
	}

	start := time.Now().UTC()
	otlpSpan := &tracepb.Span{
		TraceId:           traceIDBytes,
		SpanId:            spanIDBytes,
		Name:              "gpt-4.1",
		StartTimeUnixNano: uint64(start.UnixNano()),
		EndTimeUnixNano:   uint64(start.Add(200 * time.Millisecond).UnixNano()),
		Attributes: []*commonpb.KeyValue{
			intAttr("agentmesh.schema_version", int64(CurrentSchemaVersion)),
			strAttr("agentmesh.project_id", projectID.String()),
			strAttr("agentmesh.span_kind", string(span.KindLLMCall)),
			strAttr("agentmesh.status", string(span.StatusOK)),
		},
	}
	for _, opt := range opts {
		opt(otlpSpan)
	}
	return otlpSpan, traceID, spanID
}

func TestDecodeSpanRoundTrip(t *testing.T) {
	d := NewDecoder()
	projectID := mustProjectID(t)
	otlpSpan, wantTraceID, wantSpanID := wellFormedSpan(t, projectID)

	got, err := d.DecodeSpan(otlpSpan, projectID)
	if err != nil {
		t.Fatalf("DecodeSpan: %v", err)
	}
	if got.TraceID != wantTraceID {
		t.Errorf("TraceID = %v, want %v", got.TraceID, wantTraceID)
	}
	if got.SpanID != wantSpanID {
		t.Errorf("SpanID = %v, want %v", got.SpanID, wantSpanID)
	}
	if got.ProjectID != projectID {
		t.Errorf("ProjectID = %v, want %v", got.ProjectID, projectID)
	}
	if got.Kind != span.KindLLMCall {
		t.Errorf("Kind = %v, want %v", got.Kind, span.KindLLMCall)
	}
	if got.Status != span.StatusOK {
		t.Errorf("Status = %v, want %v", got.Status, span.StatusOK)
	}
	if got.Name != "gpt-4.1" {
		t.Errorf("Name = %q, want %q", got.Name, "gpt-4.1")
	}
	if got.EndTime.Before(got.StartTime) {
		t.Errorf("EndTime %v before StartTime %v", got.EndTime, got.StartTime)
	}
}

func TestDecodeSpanRejectsProjectIDMismatch(t *testing.T) {
	d := NewDecoder()
	claimedProjectID := mustProjectID(t)
	authenticatedProjectID := mustProjectID(t) // different project

	otlpSpan, _, _ := wellFormedSpan(t, claimedProjectID)

	_, err := d.DecodeSpan(otlpSpan, authenticatedProjectID)
	if err == nil {
		t.Fatal("DecodeSpan succeeded despite project_id mismatch, want error")
	}
}

func TestDecodeSpanRejectsSchemaVersionMismatch(t *testing.T) {
	d := NewDecoder()
	projectID := mustProjectID(t)
	otlpSpan, _, _ := wellFormedSpan(t, projectID, func(s *tracepb.Span) {
		for _, a := range s.Attributes {
			if a.Key == "agentmesh.schema_version" {
				a.Value = &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 999}}
			}
		}
	})

	_, err := d.DecodeSpan(otlpSpan, projectID)
	if err == nil {
		t.Fatal("DecodeSpan succeeded despite schema_version mismatch, want error")
	}
}

func TestDecodeSpanRejectsMissingSpanKind(t *testing.T) {
	d := NewDecoder()
	projectID := mustProjectID(t)
	otlpSpan, _, _ := wellFormedSpan(t, projectID, func(s *tracepb.Span) {
		filtered := s.Attributes[:0]
		for _, a := range s.Attributes {
			if a.Key != "agentmesh.span_kind" {
				filtered = append(filtered, a)
			}
		}
		s.Attributes = filtered
	})

	_, err := d.DecodeSpan(otlpSpan, projectID)
	if err == nil {
		t.Fatal("DecodeSpan succeeded despite missing span_kind, want error")
	}
}

func TestDecodeSpanRejectsMalformedTraceID(t *testing.T) {
	d := NewDecoder()
	projectID := mustProjectID(t)
	otlpSpan, _, _ := wellFormedSpan(t, projectID, func(s *tracepb.Span) {
		s.TraceId = []byte{1, 2, 3} // wrong length
	})

	_, err := d.DecodeSpan(otlpSpan, projectID)
	if err == nil {
		t.Fatal("DecodeSpan succeeded despite malformed trace_id, want error")
	}
}

func TestDecodeSpanCostUSDOmittedMeansNil(t *testing.T) {
	// System Design.md §7: absent cost_usd must decode to nil, never 0.0.
	d := NewDecoder()
	projectID := mustProjectID(t)
	otlpSpan, _, _ := wellFormedSpan(t, projectID)

	got, err := d.DecodeSpan(otlpSpan, projectID)
	if err != nil {
		t.Fatalf("DecodeSpan: %v", err)
	}
	if got.CostUSD != nil {
		t.Fatalf("CostUSD = %v, want nil (unknown, not zero)", *got.CostUSD)
	}
}

func TestDecodeSpanCostUSDPresent(t *testing.T) {
	d := NewDecoder()
	projectID := mustProjectID(t)
	otlpSpan, _, _ := wellFormedSpan(t, projectID, func(s *tracepb.Span) {
		s.Attributes = append(s.Attributes, doubleAttr("agentmesh.cost_usd", 0.0021))
	})

	got, err := d.DecodeSpan(otlpSpan, projectID)
	if err != nil {
		t.Fatalf("DecodeSpan: %v", err)
	}
	if got.CostUSD == nil || *got.CostUSD != 0.0021 {
		t.Fatalf("CostUSD = %v, want 0.0021", got.CostUSD)
	}
}

func TestDecodeSpanPassthroughAttributes(t *testing.T) {
	d := NewDecoder()
	projectID := mustProjectID(t)
	otlpSpan, _, _ := wellFormedSpan(t, projectID, func(s *tracepb.Span) {
		s.Attributes = append(s.Attributes, strAttr("framework", "langgraph"))
	})

	got, err := d.DecodeSpan(otlpSpan, projectID)
	if err != nil {
		t.Fatalf("DecodeSpan: %v", err)
	}
	if got.Attributes["framework"] != "langgraph" {
		t.Fatalf("Attributes[framework] = %q, want %q", got.Attributes["framework"], "langgraph")
	}
	if _, ok := got.Attributes["agentmesh.schema_version"]; ok {
		t.Fatal("agentmesh.* attributes must not leak into passthrough Attributes")
	}
}

func TestDecodeResourceSpansPartialSuccess(t *testing.T) {
	d := NewDecoder()
	projectID := mustProjectID(t)
	goodSpan, _, _ := wellFormedSpan(t, projectID)
	badSpan, _, _ := wellFormedSpan(t, projectID, func(s *tracepb.Span) {
		s.SpanId = []byte{1} // malformed
	})

	resourceSpans := []*tracepb.ResourceSpans{
		{
			ScopeSpans: []*tracepb.ScopeSpans{
				{Spans: []*tracepb.Span{goodSpan, badSpan}},
			},
		},
	}

	decoded, errs := d.DecodeResourceSpans(resourceSpans, projectID)
	if len(decoded) != 1 {
		t.Fatalf("decoded %d spans, want 1 (partial success)", len(decoded))
	}
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1", len(errs))
	}
}

func TestDecodeSpanExtractsChaosAttributesIntoDedicatedFields(t *testing.T) {
	d := NewDecoder()
	projectID := mustProjectID(t)
	otlpSpan, _, _ := wellFormedSpan(t, projectID, func(s *tracepb.Span) {
		s.Attributes = append(s.Attributes,
			strAttr("chaos.injected", "true"),
			strAttr("chaos.fault_type", "error"),
		)
	})

	got, err := d.DecodeSpan(otlpSpan, projectID)
	if err != nil {
		t.Fatalf("DecodeSpan: %v", err)
	}
	if !got.ChaosInjected {
		t.Fatal("ChaosInjected = false, want true")
	}
	if got.ChaosFaultType != "error" {
		t.Fatalf("ChaosFaultType = %q, want %q", got.ChaosFaultType, "error")
	}
}

func TestDecodeSpanChaosFieldsDefaultToZeroValueWhenAbsent(t *testing.T) {
	d := NewDecoder()
	projectID := mustProjectID(t)
	otlpSpan, _, _ := wellFormedSpan(t, projectID) // no chaos.* attributes

	got, err := d.DecodeSpan(otlpSpan, projectID)
	if err != nil {
		t.Fatalf("DecodeSpan: %v", err)
	}
	if got.ChaosInjected {
		t.Fatal("ChaosInjected = true for a span with no chaos.* attributes, want false")
	}
	if got.ChaosFaultType != "" {
		t.Fatalf("ChaosFaultType = %q, want empty string", got.ChaosFaultType)
	}
}

func TestDecodeSpanChaosAttributesAlsoAppearInPassthroughAttributes(t *testing.T) {
	// docs/otlp-mapping.md: chaos.* attributes are unprefixed and land in
	// both the dedicated fields AND the generic passthrough map, since
	// they follow the ordinary "any other attribute key" rule rather than
	// being a special-cased exception to it.
	d := NewDecoder()
	projectID := mustProjectID(t)
	otlpSpan, _, _ := wellFormedSpan(t, projectID, func(s *tracepb.Span) {
		s.Attributes = append(s.Attributes, strAttr("chaos.injected", "true"))
	})

	got, err := d.DecodeSpan(otlpSpan, projectID)
	if err != nil {
		t.Fatalf("DecodeSpan: %v", err)
	}
	if got.Attributes["chaos.injected"] != "true" {
		t.Fatalf("Attributes[chaos.injected] = %q, want %q", got.Attributes["chaos.injected"], "true")
	}
}
