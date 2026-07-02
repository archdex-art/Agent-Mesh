// Package span defines AgentMesh's core domain model: the Span and Trace
// types that every service (Collector, Query API, Replay Engine, Anomaly
// Detector, Cost Engine) shares.
//
// This is the single Go-native source of truth for the shape defined in
// System Design.md §2.1 (the ClickHouse spans table) and Architecture.md §3
// (the four span kinds and the framework-to-span mapping). Every service
// that reads or writes span data imports this package rather than declaring
// its own struct — the classic monorepo failure mode this guards against is
// two services silently drifting on what a field means (Repository
// Structure.md's rationale for keeping schema definitions out of any single
// service's ownership).
//
// SchemaVersion is embedded on every Span so a Collector or SDK version
// mismatch during a rolling upgrade is detectable rather than silently
// producing malformed data (Technical Roadmap.md §9).
package span

import (
	"fmt"
	"time"

	"github.com/agentmesh/agentmesh/shared/ids"
)

// CurrentSchemaVersion is the schema version this build of the span package
// implements. Bump it whenever the Span struct's wire-relevant shape changes
// in a way older consumers cannot safely interpret.
const CurrentSchemaVersion = 1

// Kind identifies what a Span represents, per Architecture.md §3.
type Kind string

const (
	// KindLLMCall represents a single LLM request/response.
	KindLLMCall Kind = "llm.call"
	// KindToolCall represents a single tool/function invocation.
	KindToolCall Kind = "tool.call"
	// KindAgentHandoff represents control passed from one agent/sub-agent to
	// another.
	KindAgentHandoff Kind = "agent.handoff"
	// KindMCPCall represents a tool call routed through the MCP Gateway,
	// captured automatically even if the calling agent has no SDK
	// integration.
	KindMCPCall Kind = "mcp.call"
)

// ValidKinds lists every recognized Kind, used for validation.
var ValidKinds = map[Kind]bool{
	KindLLMCall:      true,
	KindToolCall:     true,
	KindAgentHandoff: true,
	KindMCPCall:      true,
}

// Status is the terminal outcome of a Span.
type Status string

const (
	StatusOK      Status = "ok"
	StatusError   Status = "error"
	StatusTimeout Status = "timeout"
	// StatusDenied is emitted by the MCP Gateway when a guardrail policy
	// rejects a call (System Design.md §6).
	StatusDenied Status = "denied"
)

// ValidStatuses lists every recognized Status, used for validation.
var ValidStatuses = map[Status]bool{
	StatusOK:      true,
	StatusError:   true,
	StatusTimeout: true,
	StatusDenied:  true,
}

// Payload holds a span's input or output value, either inline (small
// payloads, System Design.md §2.1's 4KB threshold) or as a reference to a
// blob-store object (large payloads, Architecture.md §14). Exactly one of
// Inline or BlobRef is set; never both, per InlineOrBlobRef's invariant.
type Payload struct {
	Inline  string
	BlobRef string
}

// IsInline reports whether the payload is stored inline rather than in blob
// storage.
func (p Payload) IsInline() bool { return p.BlobRef == "" }

// IsEmpty reports whether no payload was recorded at all.
func (p Payload) IsEmpty() bool { return p.Inline == "" && p.BlobRef == "" }

// Span is one unit of work inside a Trace (System Design.md §2.1).
type Span struct {
	SchemaVersion int
	ProjectID     ids.ProjectID
	TraceID       ids.TraceID
	SpanID        ids.SpanID
	ParentSpanID  ids.SpanID // zero value means "root span"

	Kind      Kind
	Name      string // model name, tool name, or agent name depending on Kind
	StartTime time.Time
	EndTime   time.Time
	Status    Status

	Input  Payload
	Output Payload

	TokenInput  *uint32 // nil means "not applicable" (e.g., tool.call spans)
	TokenOutput *uint32
	CostUSD     *float64 // nil means "cost unknown", never assumed to be zero (System Design.md §7)

	// Attributes is the open-ended key/value bag (e.g., framework name,
	// model version) from System Design.md §2.1's `attributes Map(String,
	// String)` column.
	Attributes map[string]string

	// ChaosInjected and ChaosFaultType surface Phase 2's chaos-engineering
	// feature (sdk/python/agentmesh/chaos.py): whether this span's outcome
	// was a deliberately injected fault rather than a natural one, and
	// which kind ("latency" | "error"). Backed by
	// schema/clickhouse/002_chaos_columns.sql's dedicated columns rather
	// than the generic Attributes map, so "every span where a fault was
	// injected" is a direct, indexable column query. ChaosFaultType is
	// empty when ChaosInjected is false.
	ChaosInjected  bool
	ChaosFaultType string
}

// HasParent reports whether this Span is not a trace root.
func (s Span) HasParent() bool { return !s.ParentSpanID.IsZero() }

// Duration returns the span's wall-clock duration.
func (s Span) Duration() time.Duration { return s.EndTime.Sub(s.StartTime) }

// Validate checks the Span's structural invariants: required identifiers are
// set, Kind and Status are recognized values, EndTime is not before
// StartTime, and a Payload never sets both Inline and BlobRef. Validate is
// called by every ingestion path (Collector) before a Span is persisted, so
// malformed data is rejected at the boundary rather than corrupting the
// trace store (Architecture.md §17's error-handling philosophy).
func (s Span) Validate() error {
	if s.ProjectID == (ids.ProjectID{}) {
		return fmt.Errorf("span: ProjectID is required")
	}
	if s.TraceID.IsZero() {
		return fmt.Errorf("span: TraceID is required")
	}
	if s.SpanID.IsZero() {
		return fmt.Errorf("span: SpanID is required")
	}
	if !ValidKinds[s.Kind] {
		return fmt.Errorf("span: unrecognized kind %q", s.Kind)
	}
	if s.Name == "" {
		return fmt.Errorf("span: Name is required")
	}
	if s.Status != "" && !ValidStatuses[s.Status] {
		return fmt.Errorf("span: unrecognized status %q", s.Status)
	}
	if !s.EndTime.IsZero() && s.EndTime.Before(s.StartTime) {
		return fmt.Errorf("span: EndTime (%s) is before StartTime (%s)", s.EndTime, s.StartTime)
	}
	if !s.Input.IsEmpty() && s.Input.Inline != "" && s.Input.BlobRef != "" {
		return fmt.Errorf("span: Input payload sets both Inline and BlobRef")
	}
	if !s.Output.IsEmpty() && s.Output.Inline != "" && s.Output.BlobRef != "" {
		return fmt.Errorf("span: Output payload sets both Inline and BlobRef")
	}
	if s.SchemaVersion == 0 {
		return fmt.Errorf("span: SchemaVersion is required")
	}
	return nil
}
