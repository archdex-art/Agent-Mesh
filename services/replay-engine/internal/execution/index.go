// Package execution implements the Replay Engine's interactive replay
// mode (Architecture.md §7): re-running the *current* agent code while
// intercepting every LLM/tool call and returning the *recorded* response
// instead, via the SDK's replay shim (sdk/python/agentmesh/replay_shim.py).
//
// The interception contract (System Design.md §4, §120's "replay shim"
// section) is positional: the SDK counts, per process, how many times it
// has seen a given (kind, name) pair and asks this package for "the
// call_index'th recorded call to (kind, name)". This package's Index type
// is the server-side mirror of that same counting scheme, built once from
// a trajectory.Reconstruct() call over the source trace.
package execution

import (
	"fmt"
	"sync"

	"github.com/agentmesh/agentmesh/services/replay-engine/internal/trajectory"
	"github.com/agentmesh/agentmesh/shared/span"
)

// RecordedCall is one entry in the index: a step's resolved output plus
// its terminal status, everything the SDK's replay shim needs to either
// return a value or raise ReplayedCallError.
type RecordedCall struct {
	Output string
	Status span.Status
}

// Index maps a (kind, name) pair plus a 0-based call position to the
// recorded step from the source trace, matching exactly how
// sdk/python/agentmesh/replay_shim.py's next_call_index counts calls in
// the replaying process.
type Index struct {
	mu      sync.RWMutex
	entries map[indexKey][]RecordedCall
}

type indexKey struct {
	kind string
	name string
}

// BuildIndex constructs an Index from a trace's reconstructed steps,
// preserving each step's original chronological order within its (kind,
// name) group — the same order the SDK originally executed those calls
// in, which is what makes positional replay correct.
func BuildIndex(steps []trajectory.Step) *Index {
	idx := &Index{entries: make(map[indexKey][]RecordedCall)}
	for _, step := range steps {
		key := indexKey{kind: string(step.Span.Kind), name: step.Span.Name}
		idx.entries[key] = append(idx.entries[key], RecordedCall{
			Output: step.ResolvedOutput,
			Status: step.Span.Status,
		})
	}
	return idx
}

// ErrNoRecordedCall is returned when a replaying agent makes more calls to
// a given (kind, name) than the original trace recorded, or asks for a
// (kind, name) that never occurred in the original trace at all — a sign
// the replaying code has diverged from what was recorded (Architecture.md
// §7's determinism boundary).
var ErrNoRecordedCall = fmt.Errorf("execution: no recorded call at this position")

// Lookup returns the recorded call at callIndex for the given (kind,
// name), or ErrNoRecordedCall if the replaying agent asked for a position
// beyond what the original trace recorded.
func (idx *Index) Lookup(kind, name string, callIndex int) (RecordedCall, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	calls, ok := idx.entries[indexKey{kind: kind, name: name}]
	if !ok || callIndex < 0 || callIndex >= len(calls) {
		return RecordedCall{}, ErrNoRecordedCall
	}
	return calls[callIndex], nil
}
