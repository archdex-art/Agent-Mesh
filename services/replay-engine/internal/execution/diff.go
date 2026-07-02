package execution

import (
	"github.com/agentmesh/agentmesh/services/replay-engine/internal/trajectory"
)

// StepDiff compares one position in the original trace's (kind, name)
// sequence against the same position in the replayed run, per System
// Design.md §4's "diff current run vs. original trace" requirement.
type StepDiff struct {
	Kind           string `json:"kind"`
	Name           string `json:"name"`
	CallIndex      int    `json:"call_index"`
	OriginalStatus string `json:"original_status"`
	ReplayedStatus string `json:"replayed_status,omitempty"`
	OutputChanged  bool   `json:"output_changed"`
	// Missing is true when the replayed run never made this call at all
	// (the replaying agent code diverged before reaching it) — distinct
	// from OutputChanged, which requires both sides to have a value to
	// compare.
	Missing bool `json:"missing"`
}

// Diff is the full comparison result for one execution-mode replay run.
type Diff struct {
	Steps          []StepDiff `json:"steps"`
	ExtraCallCount int        `json:"extra_call_count"`
	IdenticalCount int        `json:"identical_count"`
	ChangedCount   int        `json:"changed_count"`
	MissingCount   int        `json:"missing_count"`
}

// Compare builds a Diff between the original trace's steps and the
// replayed run's steps. Comparison is positional within each (kind, name)
// group — the same indexing scheme the replay shim's lookup protocol uses
// — since spans across two different trace_ids have no other stable
// correspondence (a replayed run's span_ids are freshly generated, per
// tracer.py's _new_trace_id/_new_span_id).
func Compare(original, replayed []trajectory.Step) Diff {
	replayedIndex := BuildIndex(replayed)
	counters := make(map[indexKey]int)

	diff := Diff{}
	for _, step := range original {
		key := indexKey{kind: string(step.Span.Kind), name: step.Span.Name}
		callIndex := counters[key]
		counters[key]++

		sd := StepDiff{
			Kind:           key.kind,
			Name:           key.name,
			CallIndex:      callIndex,
			OriginalStatus: string(step.Span.Status),
		}

		replayedCall, err := replayedIndex.Lookup(key.kind, key.name, callIndex)
		if err != nil {
			sd.Missing = true
			diff.MissingCount++
		} else {
			sd.ReplayedStatus = string(replayedCall.Status)
			sd.OutputChanged = replayedCall.Output != step.ResolvedOutput || replayedCall.Status != step.Span.Status
			if sd.OutputChanged {
				diff.ChangedCount++
			} else {
				diff.IdenticalCount++
			}
		}
		diff.Steps = append(diff.Steps, sd)
	}

	// Any call the replayed run made beyond what the original trace has
	// for a given (kind, name) is "extra" — the replaying agent code took
	// an additional action the original run never did.
	for key, calls := range replayedIndex.entries {
		if extra := len(calls) - counters[key]; extra > 0 {
			diff.ExtraCallCount += extra
		}
	}

	return diff
}
