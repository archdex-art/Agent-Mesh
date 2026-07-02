-- Migration 002: replay-run metadata, introduced alongside Milestone 7's
-- Replay Engine (docs/plan/Milestones.md's stated M7 dependency for this
-- table). Mirrors docs/plan/System Design.md §2.2's abbreviated schema.
--
-- The Replay Engine's own working state (the ordered span list fetched
-- for a run, and the call-position index the SDK's replay shim queries)
-- lives in-process for the lifetime of one replay, not in this table —
-- Postgres here is only the durable record of "a replay happened, against
-- which trace, with what outcome," per Architecture.md §2's "writes
-- replay-run records" responsibility. Re-fetching from ClickHouse/blob
-- storage on every lookup would be wasteful, but that's an in-memory
-- engine-side cache, not a schema concern.

CREATE TABLE IF NOT EXISTS replay_runs
(
    id               UUID PRIMARY KEY,
    project_id       UUID        NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    source_trace_id  TEXT        NOT NULL,
    mode             TEXT        NOT NULL CHECK (mode IN ('trajectory', 'execution')),
    status           TEXT        NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'completed', 'failed')),
    started_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at     TIMESTAMPTZ,
    -- Free-form JSON: for execution mode, the step-by-step diff between
    -- the original trace and the replayed run (System Design.md §4:
    -- "diff current run vs. original trace"); null until the run reaches
    -- a terminal status.
    diff_summary     JSONB
);

CREATE INDEX IF NOT EXISTS idx_replay_runs_project_id ON replay_runs (project_id);

-- The Console's "replay history for this trace" view and the engine's own
-- "has this trace already been replayed" check both filter by
-- (project_id, source_trace_id) — never by source_trace_id alone, since
-- trace_id is only unique within a project (System Design.md §1).
CREATE INDEX IF NOT EXISTS idx_replay_runs_project_trace ON replay_runs (project_id, source_trace_id);
