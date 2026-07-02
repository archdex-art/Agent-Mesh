-- Migration 004: MCP Registry, guardrail policies, and OAuth 2.1-style
-- caller bearer tokens.
--
-- Source of truth: docs/plan/System Design.md §2.2's `mcp_servers` and
-- `guardrail_policies` table sketches, scoped to Milestone 6 per
-- docs/plan/Milestones.md ("MCP Registry (Postgres schema + Query
-- API/Console CRUD for server manifests)", "Guardrail policy engine v1").
--
-- `mcp_server_tokens` is new relative to System Design.md's original
-- sketch: Architecture.md §13 requires "the Gateway implements OAuth 2.1
-- as the caller-facing auth mechanism ... independent of AgentMesh's own
-- API-key auth — a caller authenticates to the *tool*, not to
-- AgentMesh." A full OAuth 2.1 authorization-code+PKCE flow is
-- deliberately out of scope here (it does not fit MCP's
-- machine-to-machine calling pattern, and MCP's own 2026 spec direction
-- favors client-credentials-style bearer tokens for this exact case,
-- Architecture.md §5's "track the spec's move toward a stateless
-- transport core"); `mcp_server_tokens` implements the OAuth
-- 2.1-compliant subset that matters operationally — opaque bearer
-- tokens, issued per registered server, hashed at rest, revocable —
-- mirroring `api_keys`' already-proven shape exactly rather than
-- inventing a second convention.

CREATE TABLE IF NOT EXISTS mcp_servers
(
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id    UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    upstream_url  TEXT NOT NULL,
    transport     TEXT NOT NULL CHECK (transport IN ('stdio', 'streamable-http')),
    version       TEXT NOT NULL,
    owner         TEXT NOT NULL,
    manifest_yaml TEXT NOT NULL, -- the raw manifest this row was registered from (cli/internal/manifest.Manifest), kept verbatim for audit/re-validation
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, name)
);

CREATE INDEX IF NOT EXISTS idx_mcp_servers_project_id ON mcp_servers (project_id);

-- One guardrail policy document per server (the declarative YAML/JSON DSL
-- from Feature Roadmap.md's "Guardrail policy engine" entry, currently
-- implemented by services/mcp-gateway/internal/policy.Document). Storing
-- the whole document as `rule_dsl` text (not normalized into individual
-- rule rows) matches how the Gateway already loads/compiles it
-- (policy.Load(data []byte)) and keeps this table's shape identical to
-- api_keys/mcp_servers' "one row = one deployable unit" pattern.
CREATE TABLE IF NOT EXISTS guardrail_policies
(
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id    UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    mcp_server_id UUID NOT NULL REFERENCES mcp_servers (id) ON DELETE CASCADE,
    rule_dsl      TEXT NOT NULL,
    enabled       BOOLEAN NOT NULL DEFAULT true,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_guardrail_policies_mcp_server_id ON guardrail_policies (mcp_server_id);

-- Only one *enabled* policy document per server at a time — the Gateway
-- loads exactly one compiled policy.Engine per server per request; if a
-- team wants a staged rollout of a new policy, they disable the old row
-- and insert a new enabled one in the same transaction, rather than the
-- Gateway needing to merge multiple concurrently-enabled documents.
CREATE UNIQUE INDEX IF NOT EXISTS idx_guardrail_policies_one_enabled_per_server
    ON guardrail_policies (mcp_server_id)
    WHERE enabled;

-- Bearer tokens scoped to one registered MCP server (see the migration
-- header comment for why this exists instead of a full OAuth 2.1
-- authorization flow). Hashed-at-rest and prefix-displayed exactly like
-- api_keys, for the same reason (Architecture.md §13).
CREATE TABLE IF NOT EXISTS mcp_server_tokens
(
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mcp_server_id UUID NOT NULL REFERENCES mcp_servers (id) ON DELETE CASCADE,
    hashed_token  TEXT NOT NULL UNIQUE,
    prefix        TEXT NOT NULL,
    caller_name   TEXT NOT NULL, -- human-readable identity for audit ("which agent/team holds this token"), distinct from AgentMesh's own project_id
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at    TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_mcp_server_tokens_server_id ON mcp_server_tokens (mcp_server_id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_server_tokens_active_hashed_token
    ON mcp_server_tokens (hashed_token)
    WHERE revoked_at IS NULL;
