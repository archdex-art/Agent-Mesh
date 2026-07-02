-- Migration 001: control-plane schema for projects and API keys.
--
-- Source of truth: docs/plan/System Design.md §2.2, scoped down per
-- docs/plan/Milestones.md Milestone 1 ("Postgres control-plane schema for
-- projects and API keys only (registry/policies/alerts added in later
-- milestones)"). Do not add mcp_servers/guardrail_policies/alert_rules/
-- replay_runs/sessions tables here — they belong to migrations introduced
-- alongside the milestones that need them (M6, M6, M6, M7, M4 respectively),
-- keeping each migration's scope traceable to the milestone that required it.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS projects
(
    id             UUID PRIMARY KEY,
    name           TEXT NOT NULL UNIQUE,
    retention_days INTEGER NOT NULL DEFAULT 90 CHECK (retention_days > 0),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- API keys are stored hashed (Architecture.md §13: "hashed at rest"); the
-- prefix (e.g. "am_live_") is stored separately in cleartext so the UI/CLI
-- can display "am_live_ab12****" for key identification without ever
-- persisting or displaying the full secret after creation.
CREATE TABLE IF NOT EXISTS api_keys
(
    id          UUID PRIMARY KEY,
    project_id  UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    hashed_key  TEXT NOT NULL UNIQUE,
    prefix      TEXT NOT NULL,
    role        TEXT NOT NULL DEFAULT 'ingest' CHECK (role IN ('ingest', 'read', 'admin')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_api_keys_project_id ON api_keys (project_id);

-- A partial index over non-revoked keys is what the Auth Service's hot path
-- (validate an incoming key on every ingestion request) actually scans.
CREATE INDEX IF NOT EXISTS idx_api_keys_active_hashed_key
    ON api_keys (hashed_key)
    WHERE revoked_at IS NULL;
