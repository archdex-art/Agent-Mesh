package store

import (
	"context"
	"encoding/json"
	"time"

	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ReplayMode is the two replay modes Architecture.md §7 defines.
type ReplayMode string

const (
	ModeTrajectory ReplayMode = "trajectory"
	ModeExecution  ReplayMode = "execution"
)

// ReplayStatus is a replay run's lifecycle state.
type ReplayStatus string

const (
	StatusRunning   ReplayStatus = "running"
	StatusCompleted ReplayStatus = "completed"
	StatusFailed    ReplayStatus = "failed"
)

// ReplayRun mirrors one row of schema/postgres/002_replay_runs.sql.
type ReplayRun struct {
	ID            ids.ReplayID
	ProjectID     ids.ProjectID
	SourceTraceID ids.TraceID
	Mode          ReplayMode
	Status        ReplayStatus
	StartedAt     time.Time
	CompletedAt   *time.Time
	DiffSummary   json.RawMessage
}

// ReplayRunStore is the Postgres-backed CRUD dependency for replay_runs.
type ReplayRunStore struct {
	pool *pgxpool.Pool
}

// NewReplayRunStore returns a ReplayRunStore backed by pool.
func NewReplayRunStore(pool *pgxpool.Pool) *ReplayRunStore {
	return &ReplayRunStore{pool: pool}
}

// Create inserts a new replay_runs row in the 'running' status, returning
// the generated ReplayID.
func (s *ReplayRunStore) Create(ctx context.Context, projectID ids.ProjectID, sourceTraceID ids.TraceID, mode ReplayMode) (ids.ReplayID, error) {
	replayID, err := ids.NewReplayID()
	if err != nil {
		return ids.ReplayID{}, amerrors.Wrap(amerrors.CodeInternal, "generating replay id", err)
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO replay_runs (id, project_id, source_trace_id, mode, status) VALUES ($1, $2, $3, $4, 'running')`,
		replayID.String(), projectID.String(), sourceTraceID.String(), string(mode),
	)
	if err != nil {
		return ids.ReplayID{}, amerrors.Wrap(amerrors.CodeUnavailable, "inserting replay_runs row", err)
	}
	return replayID, nil
}

// Complete marks a replay run as completed, attaching its diff summary
// (System Design.md §2.2's diff_summary column).
func (s *ReplayRunStore) Complete(ctx context.Context, replayID ids.ReplayID, diffSummary json.RawMessage) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE replay_runs SET status = 'completed', completed_at = now(), diff_summary = $2 WHERE id = $1`,
		replayID.String(), diffSummary,
	)
	if err != nil {
		return amerrors.Wrap(amerrors.CodeUnavailable, "completing replay_runs row", err)
	}
	return nil
}

// Fail marks a replay run as failed, recording the failure reason in
// diff_summary's free-form JSON shape (there is no separate error column;
// a failed run has no diff to show, only a reason).
func (s *ReplayRunStore) Fail(ctx context.Context, replayID ids.ReplayID, reason string) error {
	diff, _ := json.Marshal(map[string]string{"error": reason})
	_, err := s.pool.Exec(ctx,
		`UPDATE replay_runs SET status = 'failed', completed_at = now(), diff_summary = $2 WHERE id = $1`,
		replayID.String(), diff,
	)
	if err != nil {
		return amerrors.Wrap(amerrors.CodeUnavailable, "marking replay_runs row failed", err)
	}
	return nil
}

// Get retrieves a single replay run by ID, scoped to projectID so one
// project can never read another's replay history (System Design.md §1's
// project_id tenant-boundary rule applies here as everywhere else).
func (s *ReplayRunStore) Get(ctx context.Context, projectID ids.ProjectID, replayID ids.ReplayID) (ReplayRun, error) {
	var (
		idStr, projectIDStr, traceIDStr, modeStr, statusStr string
		startedAt                                           time.Time
		completedAt                                         *time.Time
		diffSummary                                         json.RawMessage
	)
	err := s.pool.QueryRow(ctx,
		`SELECT id, project_id, source_trace_id, mode, status, started_at, completed_at, diff_summary
		 FROM replay_runs WHERE id = $1 AND project_id = $2`,
		replayID.String(), projectID.String(),
	).Scan(&idStr, &projectIDStr, &traceIDStr, &modeStr, &statusStr, &startedAt, &completedAt, &diffSummary)

	if err != nil {
		if err == pgx.ErrNoRows {
			return ReplayRun{}, amerrors.New(amerrors.CodeNotFound, "no replay run matches the given id")
		}
		return ReplayRun{}, amerrors.Wrap(amerrors.CodeUnavailable, "querying replay_runs", err)
	}

	replayID, err = ids.ParseReplayID(idStr)
	if err != nil {
		return ReplayRun{}, amerrors.Wrap(amerrors.CodeInternal, "parsing replay_runs.id", err)
	}
	parsedProjectID, err := ids.ParseProjectID(projectIDStr)
	if err != nil {
		return ReplayRun{}, amerrors.Wrap(amerrors.CodeInternal, "parsing replay_runs.project_id", err)
	}
	traceID, err := ids.ParseTraceID(traceIDStr)
	if err != nil {
		return ReplayRun{}, amerrors.Wrap(amerrors.CodeInternal, "parsing replay_runs.source_trace_id", err)
	}

	return ReplayRun{
		ID:            replayID,
		ProjectID:     parsedProjectID,
		SourceTraceID: traceID,
		Mode:          ReplayMode(modeStr),
		Status:        ReplayStatus(statusStr),
		StartedAt:     startedAt,
		CompletedAt:   completedAt,
		DiffSummary:   diffSummary,
	}, nil
}
