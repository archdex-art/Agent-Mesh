package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Job kinds
const (
	KindRetentionCompaction = "retention_compaction"
)

type Job struct {
	ID        string
	ProjectID *ids.ProjectID
	Kind      string
	Payload   json.RawMessage
	Attempts  int
}

// Worker polls the jobs table using SKIP LOCKED and processes tasks.
type Worker struct {
	pgPool   *pgxpool.Pool
	chConn   driver.Conn
	logger   *slog.Logger
	pollRate time.Duration
}

func New(pgPool *pgxpool.Pool, chConn driver.Conn, logger *slog.Logger) *Worker {
	return &Worker{
		pgPool:   pgPool,
		chConn:   chConn,
		logger:   logger,
		pollRate: 5 * time.Second,
	}
}

// Run blocks and continuously polls for jobs until the context is canceled.
func (w *Worker) Run(ctx context.Context) error {
	w.logger.Info("Job worker started")
	ticker := time.NewTicker(w.pollRate)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			// Process one job per tick for simplicity at MVP scale.
			if err := w.processNextJob(ctx); err != nil {
				if !errors.Is(err, pgx.ErrNoRows) {
					w.logger.Error("Error processing job", "err", err)
				}
			}
		}
	}
}

func (w *Worker) processNextJob(ctx context.Context) error {
	tx, err := w.pgPool.Begin(ctx)
	if err != nil {
		return amerrors.Wrap(amerrors.CodeUnavailable, "begin tx", err)
	}
	defer tx.Rollback(ctx)

	// Fetch a pending job, locking it so other workers skip it.
	row := tx.QueryRow(ctx, `
		SELECT id, project_id, kind, payload, attempts 
		FROM jobs 
		WHERE status = 'pending' 
		ORDER BY created_at ASC 
		FOR UPDATE SKIP LOCKED 
		LIMIT 1
	`)

	var job Job
	var projectIDStr *string
	if err := row.Scan(&job.ID, &projectIDStr, &job.Kind, &job.Payload, &job.Attempts); err != nil {
		return err // pgx.ErrNoRows expected often
	}

	if projectIDStr != nil {
		pID, err := ids.ParseProjectID(*projectIDStr)
		if err == nil {
			job.ProjectID = &pID
		}
	}

	w.logger.Info("Acquired job", "id", job.ID, "kind", job.Kind)

	// Mark as running
	_, err = tx.Exec(ctx, `UPDATE jobs SET status = 'running', started_at = NOW(), attempts = attempts + 1 WHERE id = $1`, job.ID)
	if err != nil {
		return amerrors.Wrap(amerrors.CodeInternal, "update status to running", err)
	}

	// We must commit the transaction here to release the row lock if the job is long-running,
	// but to prevent another worker from picking it up, it's now status='running'.
	// This matches standard queue semantics without holding long DB transactions.
	if err := tx.Commit(ctx); err != nil {
		return amerrors.Wrap(amerrors.CodeInternal, "commit lock tx", err)
	}

	// Process the job
	jobErr := w.dispatch(ctx, job)

	// Record result
	ctxFinal := context.Background() // Ensure we record status even if original ctx is cancelled
	if jobErr != nil {
		w.logger.Error("Job failed", "id", job.ID, "err", jobErr)
		_, _ = w.pgPool.Exec(ctxFinal, `
			UPDATE jobs 
			SET status = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'pending' END, 
			    error_message = $2, 
			    completed_at = CASE WHEN attempts >= max_attempts THEN NOW() ELSE NULL END
			WHERE id = $1
		`, job.ID, jobErr.Error())
	} else {
		w.logger.Info("Job completed", "id", job.ID)
		_, _ = w.pgPool.Exec(ctxFinal, `UPDATE jobs SET status = 'completed', completed_at = NOW() WHERE id = $1`, job.ID)
	}

	return nil
}

func (w *Worker) dispatch(ctx context.Context, job Job) error {
	switch job.Kind {
	case KindRetentionCompaction:
		return w.handleRetentionCompaction(ctx, job)
	default:
		return fmt.Errorf("unknown job kind: %s", job.Kind)
	}
}

// handleRetentionCompaction enforces projects.retention_days via ClickHouse TTL overriding.
// Per System Design.md §2.1: TTL is overridden per-project via a scheduled compaction job
// that issues `ALTER TABLE ... MODIFY TTL` per partition.
func (w *Worker) handleRetentionCompaction(ctx context.Context, job Job) error {
	if job.ProjectID == nil {
		return fmt.Errorf("retention compaction job requires a project_id")
	}

	// Fetch retention_days from Postgres
	var retentionDays int
	err := w.pgPool.QueryRow(ctx, `SELECT retention_days FROM projects WHERE id = $1`, job.ProjectID.String()).Scan(&retentionDays)
	if err != nil {
		return amerrors.Wrap(amerrors.CodeInternal, "fetch retention_days", err)
	}

	// In ClickHouse, TTL can be updated via ALTER TABLE. However, modifying TTL dynamically
	// per project is tricky in a single table without rewriting the TTL clause or using DELETE.
	// Since ClickHouse merges parts asynchronously, lightweight DELETE is the recommended way
	// to enforce per-tenant retention in a multi-tenant table if a global TTL isn't sufficient.
	// See ClickHouse docs on Lightweight Deletes.

	query := fmt.Sprintf(`
		ALTER TABLE spans 
		DELETE WHERE project_id = '%s' AND start_time < now64(6) - INTERVAL %d DAY
	`, job.ProjectID.String(), retentionDays)

	if err := w.chConn.Exec(ctx, query); err != nil {
		return amerrors.Wrap(amerrors.CodeUnavailable, "execute CH delete", err)
	}

	return nil
}
