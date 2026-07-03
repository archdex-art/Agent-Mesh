// Package demo generates synthetic-but-realistic traces for a project,
// entirely server-side. It exists for exactly one reason: AgentMesh's
// first five minutes must never show an empty dashboard. A brand-new
// project has zero real traffic, and asking a first-time user to wire up
// the SDK before they can see what the product even does is the single
// biggest onboarding drop-off risk (Vision.md's "how does someone
// actually experience the product for the first time" gap). "Run Demo"
// and "Generate Sample Data" in the Console both call this package's
// Generate through the HTTP handler in handler.go.
//
// Every generated span still goes through the exact same path real
// spans do — writer.WriteBatch, then publisher.PublishBatch — so a demo
// trace exercises the Trace DAG viewer, Cost Dashboard, and Anomaly
// Detector identically to a real one; nothing downstream can tell the
// difference or needs a special case for it.
package demo

import (
	"fmt"
	"time"

	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/agentmesh/agentmesh/shared/span"
)

// Scenario names a canned trace shape. Unknown/empty selects ScenarioDefault.
type Scenario string

const (
	// ScenarioDefault is a clean multi-step research-assistant run: agent
	// handoff -> search tool -> LLM summarize -> read-page tool -> LLM
	// review. Exercises the DAG viewer and Replay's normal step-through.
	ScenarioDefault Scenario = "default"
	// ScenarioToolFailure fails one tool call outright (status=error) and
	// has the agent recover with a second attempt — exercises the DAG
	// viewer's error-status rendering and Replay stepping through a
	// failure.
	ScenarioToolFailure Scenario = "tool_failure"
	// ScenarioCostSpike gives one LLM call an outlier token count/cost —
	// exercises the Cost Dashboard's per-trace and per-model breakdowns
	// actually changing shape, not just growing uniformly.
	ScenarioCostSpike Scenario = "cost_spike"
	// ScenarioLoop repeats the same tool call four times in a row with
	// no progress — the exact shape the Anomaly Detector's loop tracker
	// (detector.go's loopTracker) is designed to flag once a
	// loop_detected alert rule exists for the project.
	ScenarioLoop Scenario = "loop"
)

// AllScenarios lists every scenario Generate accepts, for the Console's
// scenario picker and this package's own validation.
var AllScenarios = []Scenario{ScenarioDefault, ScenarioToolFailure, ScenarioCostSpike, ScenarioLoop}

// Generate builds one complete, internally-consistent trace (root span
// plus children, real parent/child linkage, monotonically increasing
// timestamps ending at "now") for projectID under the given scenario. An
// unrecognized scenario falls back to ScenarioDefault rather than
// erroring, since the Console always offers a fixed, validated set of
// choices — an unknown value here can only come from a stale client, not
// user input worth rejecting.
func Generate(projectID ids.ProjectID, scenario Scenario) ([]span.Span, error) {
	traceID, err := ids.NewTraceID()
	if err != nil {
		return nil, fmt.Errorf("generating trace id: %w", err)
	}

	b := &builder{projectID: projectID, traceID: traceID, now: time.Now().UTC()}

	var spans []span.Span
	switch scenario {
	case ScenarioToolFailure:
		spans, err = b.toolFailure()
	case ScenarioCostSpike:
		spans, err = b.costSpike()
	case ScenarioLoop:
		spans, err = b.loop()
	default:
		spans, err = b.happyPath()
	}
	if err != nil {
		return nil, err
	}

	// builder.add's fixed -2s start offset is sized for the shortest
	// scenario, not every scenario's summed span durations — cost_spike
	// alone runs ~14s. Rather than hand-tuning each scenario's start
	// offset (fragile: it silently goes stale the moment a span's
	// duration changes), shift the whole trace uniformly so its LAST
	// span always ends at exactly b.now, regardless of scenario. This
	// is what Generate's doc comment promises ("ending at now") and
	// what every downstream "how long ago was this trace" computation
	// in the Console assumes.
	if len(spans) > 0 {
		overflow := spans[len(spans)-1].EndTime.Sub(b.now)
		if overflow != 0 {
			for i := range spans {
				spans[i].StartTime = spans[i].StartTime.Add(-overflow)
				spans[i].EndTime = spans[i].EndTime.Add(-overflow)
			}
		}
	}
	return spans, nil
}

// builder accumulates spans for one trace, walking a virtual clock
// forward so every generated trace has a plausible, strictly-increasing
// timeline ending at "now" (real-looking in the Trace DAG's timeline
// view, not all spans stacked at a single instant).
type builder struct {
	projectID ids.ProjectID
	traceID   ids.TraceID
	now       time.Time
	cursor    time.Time
	spans     []span.Span
	set       bool
}

// add appends a new span with the given kind/name/status/duration as a
// child of parent (zero SpanID for the trace root), advancing the
// builder's cursor. tokenIn/tokenOut/costUSD are nil for kinds that
// don't carry them (tool.call, agent.handoff); pass -1 to mean nil.
func (b *builder) add(parent ids.SpanID, kind span.Kind, name string, status span.Status, dur time.Duration, tokenIn, tokenOut int, costUSD float64, attrs map[string]string) (ids.SpanID, error) {
	if !b.set {
		b.cursor = b.now.Add(-2 * time.Second) // trace starts ~2s before "now"
		b.set = true
	}
	spanID, err := ids.NewSpanID()
	if err != nil {
		return ids.SpanID{}, fmt.Errorf("generating span id: %w", err)
	}
	start := b.cursor
	end := start.Add(dur)
	b.cursor = end

	s := span.Span{
		SchemaVersion: span.CurrentSchemaVersion,
		ProjectID:     b.projectID,
		TraceID:       b.traceID,
		SpanID:        spanID,
		ParentSpanID:  parent,
		Kind:          kind,
		Name:          name,
		StartTime:     start,
		EndTime:       end,
		Status:        status,
		Input:         span.Payload{Inline: attrs["_input"]},
		Output:        span.Payload{Inline: attrs["_output"]},
		Attributes:    attrs,
	}
	delete(s.Attributes, "_input")
	delete(s.Attributes, "_output")
	if tokenIn >= 0 {
		v := uint32(tokenIn)
		s.TokenInput = &v
	}
	if tokenOut >= 0 {
		v := uint32(tokenOut)
		s.TokenOutput = &v
	}
	if costUSD >= 0 {
		s.CostUSD = &costUSD
	}
	b.spans = append(b.spans, s)
	return spanID, nil
}

func demoAttrs(framework string, extra map[string]string) map[string]string {
	attrs := map[string]string{"framework": framework, "demo": "true"}
	for k, v := range extra {
		attrs[k] = v
	}
	return attrs
}

func (b *builder) happyPath() ([]span.Span, error) {
	root, err := b.add(ids.SpanID{}, span.KindAgentHandoff, "research-assistant", span.StatusOK, 2100*time.Millisecond, -1, -1, -1,
		demoAttrs("langgraph", map[string]string{"_input": `{"topic":"quarterly infra spend"}`}))
	if err != nil {
		return nil, err
	}
	if _, err := b.add(root, span.KindToolCall, "web_search", span.StatusOK, 340*time.Millisecond, -1, -1, -1,
		demoAttrs("langgraph", map[string]string{"_input": `{"query":"quarterly infra spend trends"}`, "_output": `{"results":3}`})); err != nil {
		return nil, err
	}
	if _, err := b.add(root, span.KindLLMCall, "gpt-4.1", span.StatusOK, 890*time.Millisecond, 512, 128, 0.0068,
		demoAttrs("langgraph", map[string]string{"model": "gpt-4.1"})); err != nil {
		return nil, err
	}
	if _, err := b.add(root, span.KindToolCall, "read_page", span.StatusOK, 210*time.Millisecond, -1, -1, -1,
		demoAttrs("langgraph", map[string]string{"_input": `{"url":"https://example.com/report"}`})); err != nil {
		return nil, err
	}
	if _, err := b.add(root, span.KindLLMCall, "gpt-4.1", span.StatusOK, 560*time.Millisecond, 780, 96, 0.0091,
		demoAttrs("langgraph", map[string]string{"model": "gpt-4.1", "_output": `{"summary":"Infra spend up 12% QoQ, driven by ClickHouse storage."}`})); err != nil {
		return nil, err
	}
	return b.spans, nil
}

func (b *builder) toolFailure() ([]span.Span, error) {
	root, err := b.add(ids.SpanID{}, span.KindAgentHandoff, "support-bot", span.StatusOK, 2600*time.Millisecond, -1, -1, -1,
		demoAttrs("crewai", map[string]string{"_input": `{"ticket":"CRM sync failing for account 4821"}`}))
	if err != nil {
		return nil, err
	}
	if _, err := b.add(root, span.KindToolCall, "crm_lookup", span.StatusError, 5000*time.Millisecond, -1, -1, -1,
		demoAttrs("crewai", map[string]string{"_output": `{"error":"upstream 503"}`})); err != nil {
		return nil, err
	}
	if _, err := b.add(root, span.KindLLMCall, "gpt-4.1", span.StatusOK, 410*time.Millisecond, 340, 64, 0.0041,
		demoAttrs("crewai", map[string]string{"model": "gpt-4.1", "_output": `{"decision":"retry with cached snapshot"}`})); err != nil {
		return nil, err
	}
	if _, err := b.add(root, span.KindToolCall, "crm_lookup", span.StatusOK, 280*time.Millisecond, -1, -1, -1,
		demoAttrs("crewai", map[string]string{"_output": `{"account":4821,"status":"synced"}`})); err != nil {
		return nil, err
	}
	if _, err := b.add(root, span.KindLLMCall, "gpt-4.1", span.StatusOK, 470*time.Millisecond, 290, 88, 0.0038,
		demoAttrs("crewai", map[string]string{"model": "gpt-4.1", "_output": `{"reply":"Resolved via cached snapshot after a transient upstream 503."}`})); err != nil {
		return nil, err
	}
	return b.spans, nil
}

func (b *builder) costSpike() ([]span.Span, error) {
	root, err := b.add(ids.SpanID{}, span.KindAgentHandoff, "report-generator", span.StatusOK, 8200*time.Millisecond, -1, -1, -1,
		demoAttrs("openai-agents-sdk", map[string]string{"_input": `{"doc_count":40}`}))
	if err != nil {
		return nil, err
	}
	if _, err := b.add(root, span.KindToolCall, "fetch_documents", span.StatusOK, 900*time.Millisecond, -1, -1, -1,
		demoAttrs("openai-agents-sdk", map[string]string{"_output": `{"documents":40}`})); err != nil {
		return nil, err
	}
	// The outlier: a single call summarizing all 40 documents in one
	// shot instead of batching — the realistic mistake this scenario
	// exists to make visible on the Cost Dashboard.
	if _, err := b.add(root, span.KindLLMCall, "gpt-4.1", span.StatusOK, 6800*time.Millisecond, 128000, 4096, 2.184,
		demoAttrs("openai-agents-sdk", map[string]string{"model": "gpt-4.1", "note": "unbatched full-corpus summarization"})); err != nil {
		return nil, err
	}
	if _, err := b.add(root, span.KindLLMCall, "gpt-4.1", span.StatusOK, 500*time.Millisecond, 220, 60, 0.0034,
		demoAttrs("openai-agents-sdk", map[string]string{"model": "gpt-4.1", "_output": `{"final_report":"..."}`})); err != nil {
		return nil, err
	}
	return b.spans, nil
}

func (b *builder) loop() ([]span.Span, error) {
	root, err := b.add(ids.SpanID{}, span.KindAgentHandoff, "shopping-assistant", span.StatusOK, 6000*time.Millisecond, -1, -1, -1,
		demoAttrs("autogen", map[string]string{"_input": `{"item":"wireless keyboard under $40"}`}))
	if err != nil {
		return nil, err
	}
	// Same tool, same arguments, four times straight — no new
	// information between calls, exactly what loopTracker's
	// consecutive-identical-kind+name counting is built to catch.
	for i := range 4 {
		if _, err := b.add(root, span.KindToolCall, "search_products", span.StatusOK, 350*time.Millisecond, -1, -1, -1,
			demoAttrs("autogen", map[string]string{"_input": `{"query":"wireless keyboard under $40"}`, "attempt": fmt.Sprint(i + 1)})); err != nil {
			return nil, err
		}
	}
	if _, err := b.add(root, span.KindLLMCall, "gpt-4.1", span.StatusError, 300*time.Millisecond, 640, 12, 0.0052,
		demoAttrs("autogen", map[string]string{"model": "gpt-4.1", "_output": `{"error":"gave up after repeated identical search calls"}`})); err != nil {
		return nil, err
	}
	return b.spans, nil
}
