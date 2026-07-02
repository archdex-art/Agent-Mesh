package execution

import (
	"testing"

	"github.com/agentmesh/agentmesh/services/replay-engine/internal/trajectory"
	"github.com/agentmesh/agentmesh/shared/span"
)

func step(kind span.Kind, name, output string, status span.Status) trajectory.Step {
	return trajectory.Step{
		Span:           span.Span{Kind: kind, Name: name, Status: status},
		ResolvedOutput: output,
	}
}

func TestBuildIndexGroupsByKindAndNamePreservingOrder(t *testing.T) {
	steps := []trajectory.Step{
		step(span.KindToolCall, "search", "result1", span.StatusOK),
		step(span.KindLLMCall, "gpt-4.1", "completion1", span.StatusOK),
		step(span.KindToolCall, "search", "result2", span.StatusOK),
	}
	idx := BuildIndex(steps)

	call0, err := idx.Lookup("tool.call", "search", 0)
	if err != nil {
		t.Fatalf("Lookup(search, 0): %v", err)
	}
	if call0.Output != "result1" {
		t.Fatalf("call0.Output = %q, want %q", call0.Output, "result1")
	}

	call1, err := idx.Lookup("tool.call", "search", 1)
	if err != nil {
		t.Fatalf("Lookup(search, 1): %v", err)
	}
	if call1.Output != "result2" {
		t.Fatalf("call1.Output = %q, want %q", call1.Output, "result2")
	}

	llmCall, err := idx.Lookup("llm.call", "gpt-4.1", 0)
	if err != nil {
		t.Fatalf("Lookup(gpt-4.1, 0): %v", err)
	}
	if llmCall.Output != "completion1" {
		t.Fatalf("llmCall.Output = %q, want %q", llmCall.Output, "completion1")
	}
}

func TestLookupReturnsErrNoRecordedCallForUnknownName(t *testing.T) {
	idx := BuildIndex([]trajectory.Step{step(span.KindToolCall, "search", "r", span.StatusOK)})
	if _, err := idx.Lookup("tool.call", "fetch", 0); err != ErrNoRecordedCall {
		t.Fatalf("Lookup(fetch, 0) error = %v, want ErrNoRecordedCall", err)
	}
}

func TestLookupReturnsErrNoRecordedCallForOutOfRangeIndex(t *testing.T) {
	idx := BuildIndex([]trajectory.Step{step(span.KindToolCall, "search", "r", span.StatusOK)})
	if _, err := idx.Lookup("tool.call", "search", 1); err != ErrNoRecordedCall {
		t.Fatalf("Lookup(search, 1) error = %v, want ErrNoRecordedCall (only 1 recorded call)", err)
	}
}

func TestLookupReturnsErrNoRecordedCallForNegativeIndex(t *testing.T) {
	idx := BuildIndex([]trajectory.Step{step(span.KindToolCall, "search", "r", span.StatusOK)})
	if _, err := idx.Lookup("tool.call", "search", -1); err != ErrNoRecordedCall {
		t.Fatalf("Lookup(search, -1) error = %v, want ErrNoRecordedCall", err)
	}
}

func TestLookupPreservesRecordedErrorStatus(t *testing.T) {
	idx := BuildIndex([]trajectory.Step{step(span.KindToolCall, "search", "upstream 500", span.StatusError)})
	call, err := idx.Lookup("tool.call", "search", 0)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if call.Status != span.StatusError {
		t.Fatalf("call.Status = %v, want StatusError", call.Status)
	}
}

func TestBuildIndexHandlesEmptySteps(t *testing.T) {
	idx := BuildIndex(nil)
	if _, err := idx.Lookup("tool.call", "search", 0); err != ErrNoRecordedCall {
		t.Fatalf("Lookup on empty index error = %v, want ErrNoRecordedCall", err)
	}
}
