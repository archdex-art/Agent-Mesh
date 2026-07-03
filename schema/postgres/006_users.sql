-- Migration 006: user accounts and session-based auth for the hosted
-- Web Console.
--
-- Prior to this migration, the only way to get a project + API key was
-- the anonymous `POST /v1/setup` endpoint (one click, no identity) — fine
-- for a local self-hosted eval, but not what "a website I can log into"
-- means. This migration adds real accounts on top of the EXISTING
-- projects/api_keys model without changing it: a user owns N projects,
-- each project still has its own API keys exactly as before (ingestion
-- and every other service's auth path is completely unaffected).

CREATE TABLE IF NOT EXISTS users
(
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email           TEXT NOT NULL UNIQUE,
    hashed_password TEXT NOT NULL, -- bcrypt, unlike api_keys' SHA-256: passwords are low-entropy human secrets, so a slow KDF's brute-force resistance matters here (the opposite tradeoff authkeys.Hash's doc comment explains for API keys)
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Session tokens are opaque, hashed at rest, exactly like api_keys and
-- mcp_server_tokens — the same convention repeated a third time rather
-- than reinventing "how do we store a bearer secret" again.
CREATE TABLE IF NOT EXISTS sessions
(
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_sessions_active_token_hash
    ON sessions (token_hash)
    WHERE revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions (user_id);

-- A project now optionally belongs to a user (nullable so every
-- pre-existing row from anonymous `/v1/setup` calls, and every
-- integration-test-created project, remains valid without a backfill).
ALTER TABLE projects ADD COLUMN IF NOT EXISTS owner_user_id UUID REFERENCES users (id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_projects_owner_user_id ON projects (owner_user_id);
