package demo

import (
	"testing"
	"time"

	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/agentmesh/agentmesh/shared/span"
)

// mustProjectID generates a fresh ProjectID for a test, failing the test
// immediately if generation errors (mirrors
// shared/authkeys/authkeys_test.go's helper of the same name; this copy
// lives in package demo since Go test helpers aren't shared across
// packages).
func mustProjectID(t *testing.T) ids.ProjectID {
	t.Helper()
	id, err := ids.NewProjectID()
	if err != nil {
		t.Fatalf("NewProjectID: %v", err)
	}
	return id
}

// TestGenerateProducesValidTraceForEveryScenario walks every scenario in
// AllScenarios and checks the structural invariants Generate promises: a
// single root span, a single-level parent/child tree under it, consistent
// ProjectID/TraceID across every span, sane timestamps, and that every
// generated span independently passes span.Validate() the same way the
// real ingestion path would check it.
func TestGenerateProducesValidTraceForEveryScenario(t *testing.T) {
	for _, scenario := range AllScenarios {
		t.Run(string(scenario), func(t *testing.T) {
			projectID := mustProjectID(t)
			spans, err := Generate(projectID, scenario)
			if err != nil {
				t.Fatalf("Generate(%q): %v", scenario, err)
			}
			if len(spans) == 0 {
				t.Fatalf("Generate(%q) produced no spans", scenario)
			}

			var root span.Span
			rootCount := 0
			for _, s := range spans {
				if s.ParentSpanID.IsZero() {
					rootCount++
					root = s
				}
			}
			if rootCount != 1 {
				t.Fatalf("Generate(%q) produced %d root spans (zero ParentSpanID), want exactly 1", scenario, rootCount)
			}

			traceID := spans[0].TraceID
			var prevStart, prevEnd time.Time
			for i, s := range spans {
				if err := s.Validate(); err != nil {
					t.Fatalf("spans[%d] (%s/%s).Validate(): %v", i, s.Kind, s.Name, err)
				}
				if s.ProjectID != projectID {
					t.Fatalf("spans[%d].ProjectID = %v, want %v", i, s.ProjectID, projectID)
				}
				if s.TraceID != traceID {
					t.Fatalf("spans[%d].TraceID = %v, want %v (every span in a trace shares one TraceID)", i, s.TraceID, traceID)
				}
				if s.EndTime.Before(s.StartTime) {
					t.Fatalf("spans[%d]: EndTime %v is before StartTime %v", i, s.EndTime, s.StartTime)
				}
				if !s.ParentSpanID.IsZero() && s.ParentSpanID != root.SpanID {
					t.Fatalf("spans[%d].ParentSpanID = %v, want root's SpanID %v (single-level tree)", i, s.ParentSpanID, root.SpanID)
				}
				if i > 0 {
					if s.StartTime.Before(prevStart) {
						t.Fatalf("spans[%d].StartTime %v is before spans[%d].StartTime %v; want non-decreasing generation order", i, s.StartTime, i-1, prevStart)
					}
					if s.EndTime.Before(prevEnd) {
						t.Fatalf("spans[%d].EndTime %v is before spans[%d].EndTime %v; want non-decreasing generation order", i, s.EndTime, i-1, prevEnd)
					}
				}
				prevStart, prevEnd = s.StartTime, s.EndTime
			}
		})
	}
}

// TestToolFailureScenarioIncludesAnErrorSpan checks ScenarioToolFailure's
// defining trait: at least one span records a failed call, exercising the
// DAG viewer's error-status rendering per scenario.go's doc comment.
func TestToolFailureScenarioIncludesAnErrorSpan(t *testing.T) {
	spans, err := Generate(mustProjectID(t), ScenarioToolFailure)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, s := range spans {
		if s.Status == span.StatusError {
			return
		}
	}
	t.Fatalf("ScenarioToolFailure produced no span.StatusError span among %d spans", len(spans))
}

// TestCostSpikeScenarioIncludesAnOutlierCostSpan checks ScenarioCostSpike's
// defining trait: one LLM call with an outlier CostUSD, the shape the Cost
// Dashboard test exists to make visible.
func TestCostSpikeScenarioIncludesAnOutlierCostSpan(t *testing.T) {
	spans, err := Generate(mustProjectID(t), ScenarioCostSpike)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, s := range spans {
		if s.CostUSD != nil && *s.CostUSD > 1.0 {
			return
		}
	}
	t.Fatalf("ScenarioCostSpike produced no span with CostUSD > 1.0 among %d spans", len(spans))
}

// TestLoopScenarioRepeatsSameToolCallFourTimes checks ScenarioLoop's
// defining trait: at least four consecutive tool.call children of the
// root, all named "search_products" — the exact shape the Anomaly
// Detector's loopTracker is built to flag.
func TestLoopScenarioRepeatsSameToolCallFourTimes(t *testing.T) {
	spans, err := Generate(mustProjectID(t), ScenarioLoop)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var root span.Span
	for _, s := range spans {
		if s.ParentSpanID.IsZero() {
			root = s
			break
		}
	}
	if root.SpanID.IsZero() {
		t.Fatalf("ScenarioLoop produced no root span")
	}

	const wantName = "search_products"
	run, maxRun := 0, 0
	for _, s := range spans {
		if s.Kind == span.KindToolCall && s.Name == wantName && s.ParentSpanID == root.SpanID {
			run++
			if run > maxRun {
				maxRun = run
			}
		} else {
			run = 0
		}
	}
	if maxRun < 4 {
		t.Fatalf("ScenarioLoop's longest run of consecutive %q tool.call children of the root is %d, want >= 4", wantName, maxRun)
	}
}

// TestGenerateUnrecognizedScenarioFallsBackToDefaultSpanCount checks
// Generate's documented fallback: an unrecognized or empty Scenario value
// behaves like ScenarioDefault rather than erroring.
func TestGenerateUnrecognizedScenarioFallsBackToDefaultSpanCount(t *testing.T) {
	projectID := mustProjectID(t)

	defaultSpans, err := Generate(projectID, ScenarioDefault)
	if err != nil {
		t.Fatalf("Generate(ScenarioDefault): %v", err)
	}

	for _, scenario := range []Scenario{"", "not-a-real-scenario", Scenario("BOGUS")} {
		spans, err := Generate(projectID, scenario)
		if err != nil {
			t.Fatalf("Generate(%q): %v", scenario, err)
		}
		if len(spans) != len(defaultSpans) {
			t.Fatalf("Generate(%q) produced %d spans, want %d (same as ScenarioDefault)", scenario, len(spans), len(defaultSpans))
		}
	}
}
