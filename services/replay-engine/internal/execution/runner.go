package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/agentmesh/agentmesh/services/replay-engine/internal/store"
	"github.com/agentmesh/agentmesh/services/replay-engine/internal/trajectory"
	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/agentmesh/agentmesh/shared/span"
)

// SpanReader is the read dependency Runner needs: reconstructing the
// source trace (trajectory.SpanReader) plus finding a completed replay
// run's own spans by the `replay.replay_id` tag every span carries during
// an active replay (tracer.py's Tracer.start_span).
type SpanReader interface {
	trajectory.SpanReader
	GetSpansByReplayID(ctx context.Context, projectID ids.ProjectID, replayID string) ([]span.Span, error)
}

// RunStore is the Postgres-backed replay_runs CRUD dependency (see
// internal/store.ReplayRunStore for the production implementation).
type RunStore interface {
	Create(ctx context.Context, projectID ids.ProjectID, sourceTraceID ids.TraceID, mode store.ReplayMode) (ids.ReplayID, error)
	Get(ctx context.Context, projectID ids.ProjectID, replayID ids.ReplayID) (store.ReplayRun, error)
	Complete(ctx context.Context, replayID ids.ReplayID, diffSummary json.RawMessage) error
	Fail(ctx context.Context, replayID ids.ReplayID, reason string) error
}

// activeRun holds the in-process state for one in-flight execution-mode
// replay: the recorded-response index the SDK's lookup calls query
// (rebuilding it from ClickHouse/blob storage on every single tool-call
// lookup would make replay unusably slow — a real agent can make dozens
// of calls per run) and the original trace's steps, needed again at
// Complete time to compute the diff.
type activeRun struct {
	projectID     ids.ProjectID
	sourceTraceID ids.TraceID
	originalSteps []trajectory.Step
	index         *Index
}

// Runner orchestrates execution-mode replay end to end: starting a run
// (fetch + index the source trace), answering the SDK's positional lookup
// calls while the replaying agent process runs, and computing/persisting
// the diff once that process reports completion (Architecture.md §10:
// the `agentmesh replay` CLI command drives the actual agent process; this
// type is what it talks to over HTTP via internal/rest).
type Runner struct {
	spanReader SpanReader
	blobs      trajectory.BlobStore
	runs       RunStore

	mu     sync.Mutex
	active map[ids.ReplayID]*activeRun
}

// NewRunner returns a Runner backed by the given dependencies.
func NewRunner(spanReader SpanReader, blobs trajectory.BlobStore, runs RunStore) *Runner {
	return &Runner{spanReader: spanReader, blobs: blobs, runs: runs, active: make(map[ids.ReplayID]*activeRun)}
}

// Start begins an execution-mode replay: reconstructs the source trace,
// builds its recorded-response index, records a 'running' replay_runs
// row, and returns the new ReplayID for the caller (the CLI) to export as
// AGENTMESH_REPLAY_ID before invoking the agent process.
func (r *Runner) Start(ctx context.Context, projectID ids.ProjectID, sourceTraceID ids.TraceID) (ids.ReplayID, error) {
	steps, err := trajectory.Reconstruct(ctx, r.spanReader, r.blobs, projectID, sourceTraceID)
	if err != nil {
		return ids.ReplayID{}, err
	}

	replayID, err := r.runs.Create(ctx, projectID, sourceTraceID, store.ModeExecution)
	if err != nil {
		return ids.ReplayID{}, err
	}

	r.mu.Lock()
	r.active[replayID] = &activeRun{
		projectID:     projectID,
		sourceTraceID: sourceTraceID,
		originalSteps: steps,
		index:         BuildIndex(steps),
	}
	r.mu.Unlock()

	return replayID, nil
}

// ErrUnknownReplayRun is returned when a lookup or complete call names a
// ReplayID that was never started in this process (e.g., the engine
// restarted mid-replay, or the caller has a stale ID).
var ErrUnknownReplayRun = fmt.Errorf("execution: unknown or expired replay run")

// Lookup answers the SDK replay shim's positional call, per
// sdk/python/agentmesh/replay_shim.py's fetch_recorded_response contract.
func (r *Runner) Lookup(replayID ids.ReplayID, kind, name string, callIndex int) (RecordedCall, error) {
	r.mu.Lock()
	run, ok := r.active[replayID]
	r.mu.Unlock()
	if !ok {
		return RecordedCall{}, ErrUnknownReplayRun
	}
	return run.index.Lookup(kind, name, callIndex)
}

// Complete fetches the replayed agent process's own spans, computes the
// diff against the original trace, persists it, and marks the run
// completed. Called by the CLI once the replayed agent process has
// exited (Architecture.md §10).
func (r *Runner) Complete(ctx context.Context, projectID ids.ProjectID, replayID ids.ReplayID) (Diff, error) {
	r.mu.Lock()
	run, ok := r.active[replayID]
	r.mu.Unlock()
	if !ok {
		return Diff{}, ErrUnknownReplayRun
	}

	replayedSpans, err := r.spanReader.GetSpansByReplayID(ctx, projectID, replayID.String())
	if err != nil {
		_ = r.runs.Fail(ctx, replayID, err.Error())
		return Diff{}, err
	}
	if len(replayedSpans) == 0 {
		reason := "no spans were tagged with this replay_id — the replayed agent process may not have run, or ran without AGENTMESH_REPLAY_ID set"
		_ = r.runs.Fail(ctx, replayID, reason)
		return Diff{}, amerrors.New(amerrors.CodeNotFound, reason)
	}

	replayedSteps := make([]trajectory.Step, 0, len(replayedSpans))
	for _, s := range replayedSpans {
		input, err := trajectory.ResolvePayload(ctx, r.blobs, s.Input)
		if err != nil {
			_ = r.runs.Fail(ctx, replayID, err.Error())
			return Diff{}, err
		}
		output, err := trajectory.ResolvePayload(ctx, r.blobs, s.Output)
		if err != nil {
			_ = r.runs.Fail(ctx, replayID, err.Error())
			return Diff{}, err
		}
		replayedSteps = append(replayedSteps, trajectory.Step{Span: s, ResolvedInput: input, ResolvedOutput: output})
	}

	diff := Compare(run.originalSteps, replayedSteps)
	diffJSON, err := json.Marshal(diff)
	if err != nil {
		return Diff{}, amerrors.Wrap(amerrors.CodeInternal, "marshaling replay diff", err)
	}
	if err := r.runs.Complete(ctx, replayID, diffJSON); err != nil {
		return Diff{}, err
	}

	r.mu.Lock()
	delete(r.active, replayID)
	r.mu.Unlock()

	return diff, nil
}
