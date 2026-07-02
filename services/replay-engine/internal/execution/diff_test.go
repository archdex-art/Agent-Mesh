package execution

import (
	"testing"

	"github.com/agentmesh/agentmesh/services/replay-engine/internal/trajectory"
	"github.com/agentmesh/agentmesh/shared/span"
)

func origStep(kind span.Kind, name, output string, status span.Status) trajectory.Step {
	return trajectory.Step{
		Span:           span.Span{Kind: kind, Name: name, Status: status},
		ResolvedOutput: output,
	}
}

func TestCompareIdenticalRunsProducesNoChanges(t *testing.T) {
	original := []trajectory.Step{
		origStep(span.KindToolCall, "search", "result1", span.StatusOK),
		origStep(span.KindLLMCall, "gpt-4.1", "completion1", span.StatusOK),
	}
	replayed := []trajectory.Step{
		origStep(span.KindToolCall, "search", "result1", span.StatusOK),
		origStep(span.KindLLMCall, "gpt-4.1", "completion1", span.StatusOK),
	}

	diff := Compare(original, replayed)

	if diff.IdenticalCount != 2 {
		t.Fatalf("IdenticalCount = %d, want 2", diff.IdenticalCount)
	}
	if diff.ChangedCount != 0 || diff.MissingCount != 0 || diff.ExtraCallCount != 0 {
		t.Fatalf("unexpected diff = %+v", diff)
	}
}

func TestCompareDetectsChangedOutput(t *testing.T) {
	original := []trajectory.Step{origStep(span.KindToolCall, "search", "old result", span.StatusOK)}
	replayed := []trajectory.Step{origStep(span.KindToolCall, "search", "new result", span.StatusOK)}

	diff := Compare(original, replayed)

	if diff.ChangedCount != 1 {
		t.Fatalf("ChangedCount = %d, want 1", diff.ChangedCount)
	}
	if !diff.Steps[0].OutputChanged {
		t.Fatal("Steps[0].OutputChanged = false, want true")
	}
}

func TestCompareDetectsChangedStatusEvenWithSameOutput(t *testing.T) {
	original := []trajectory.Step{origStep(span.KindToolCall, "search", "result", span.StatusOK)}
	replayed := []trajectory.Step{origStep(span.KindToolCall, "search", "result", span.StatusError)}

	diff := Compare(original, replayed)

	if diff.ChangedCount != 1 {
		t.Fatalf("ChangedCount = %d, want 1 (status differs even though output text matches)", diff.ChangedCount)
	}
}

func TestCompareDetectsMissingCallWhenReplayNeverMadeIt(t *testing.T) {
	original := []trajectory.Step{
		origStep(span.KindToolCall, "search", "result1", span.StatusOK),
		origStep(span.KindToolCall, "search", "result2", span.StatusOK),
	}
	// Replayed run only made the call once — the agent code diverged
	// before making the second search.
	replayed := []trajectory.Step{
		origStep(span.KindToolCall, "search", "result1", span.StatusOK),
	}

	diff := Compare(original, replayed)

	if diff.MissingCount != 1 {
		t.Fatalf("MissingCount = %d, want 1", diff.MissingCount)
	}
	if !diff.Steps[1].Missing {
		t.Fatal("Steps[1].Missing = false, want true")
	}
}

func TestCompareDetectsExtraCallBeyondOriginal(t *testing.T) {
	original := []trajectory.Step{origStep(span.KindToolCall, "search", "result1", span.StatusOK)}
	replayed := []trajectory.Step{
		origStep(span.KindToolCall, "search", "result1", span.StatusOK),
		origStep(span.KindToolCall, "search", "unexpected extra call", span.StatusOK),
	}

	diff := Compare(original, replayed)

	if diff.ExtraCallCount != 1 {
		t.Fatalf("ExtraCallCount = %d, want 1", diff.ExtraCallCount)
	}
	if diff.IdenticalCount != 1 {
		t.Fatalf("IdenticalCount = %d, want 1", diff.IdenticalCount)
	}
}

func TestCompareHandlesEmptyOriginal(t *testing.T) {
	diff := Compare(nil, []trajectory.Step{origStep(span.KindToolCall, "search", "r", span.StatusOK)})
	if len(diff.Steps) != 0 {
		t.Fatalf("len(Steps) = %d, want 0 for empty original", len(diff.Steps))
	}
	if diff.ExtraCallCount != 1 {
		t.Fatalf("ExtraCallCount = %d, want 1", diff.ExtraCallCount)
	}
}

func TestCompareHandlesEmptyReplayed(t *testing.T) {
	diff := Compare([]trajectory.Step{origStep(span.KindToolCall, "search", "r", span.StatusOK)}, nil)
	if diff.MissingCount != 1 {
		t.Fatalf("MissingCount = %d, want 1 for empty replayed run", diff.MissingCount)
	}
}
