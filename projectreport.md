# AgentMesh Project Report

**Date:** July 2026  
**Status:** 100% Complete (Milestone 8 of 8) + Hosted Auth (Post-Milestone)

## Executive Summary
AgentMesh is a framework-agnostic control plane for AI agents that provides execution tracing, deterministic replay, MCP-native governance, and cost intelligence. 
The project has successfully laid its foundational data models, established the telemetry ingestion pipeline, completed reference integrations for the top four agent frameworks, crossed the MVP ship line with the Memory System and Web Console, shipped the terminal/CLI experience with live realtime tracing, delivered the MCP Registry + Gateway governance layer, shipped AI Workflows (Replay & Anomalies), finalized production polish (Helm charts, documentation), and added real user accounts so the Web Console is now a genuine hosted-product login experience, not just a self-hosted eval tool.

---

## 🟢 Post-Milestone: Hosted Auth (User Accounts)
Added on top of the completed 8-milestone roadmap, in direct response to the ask "I want a website where I can use this." Turns the Console from "click one button, get an anonymous project" into a real sign-up/log-in product, while changing nothing about how the existing project/API-key/ingestion model works underneath.
* **Schema:** New `users` and `sessions` tables (`schema/postgres/006_users.sql`); `projects.owner_user_id` added as a nullable FK so every pre-existing anonymous/test project stays valid with zero backfill.
* **Query API:** `POST /v1/auth/register`, `POST /v1/auth/login` (bcrypt-hashed passwords, opaque session tokens), `GET /v1/auth/me`, `GET/POST /v1/auth/projects` — a second, additive auth layer (`Authorization: Bearer <session_token>`) that never touches the existing API-key auth (`X-AgentMesh-API-Key`) every other endpoint uses.
* **Web Console:** Real Sign Up / Log In screens, a project picker/creator, and a "Log out" flow — with the original anonymous `/v1/setup` path kept alive behind a "Continue without an account" link for zero-signup local eval. Live-verified in a real browser: signed up a brand-new user, created a project, confirmed the existing Traces/Cost/Registry views render unchanged, logged out cleanly.
* **CLI:** `agentmesh login` prompts for email/password (hidden input via `golang.org/x/term`), stores a session + resolved API key in `~/.agentmesh/config.json` (mode `0600`). `tail`/`mcp register` now fall back to this file when `--api-key`/`$AGENTMESH_API_KEY` aren't set — verified end-to-end with a real login-produced config file.
* **Two real bugs found and fixed during this work:** (1) `setup.go`'s default project name truncated a UUIDv7's time-ordered prefix, so any two projects created within the same ~65-second window collided on the `UNIQUE(name)` constraint — fixed by sourcing the default name from the UUID's random tail instead; (2) a `pflag`/cobra quirk where backtick-wrapped text in a flag's help string gets interpreted as a value-placeholder override, corrupting `--help` output — fixed by switching to single-quotes.
* **Verification:** All new Go tests (24 across `services/query-api` and `cli`) pass against live Postgres; zero regressions across every pre-existing test in both modules; full browser-driven end-to-end flow (signup → login → create project → use app → logout) verified live, not just asserted.

---

## 🟢 Completed Work (Milestones 1–8)

### Milestone 1: Foundation
* **Monorepo Architecture:** Scaffolded the Go services, Python/TS SDKs, web apps, and schema definitions.
* **Data Model:** Versioned ClickHouse span schema and Postgres control-plane schema implemented.
* **Infrastructure:** `docker-compose.yml` local dev environment configured (Postgres, ClickHouse, Redis, MinIO).
* **Cross-Service Primitives:** Shared Go packages (`span`, `ids`, `errors`, `logging`, `config`) ensuring a single source of truth for the domain model.

### Milestone 2: Core Engine
* **Python SDK (Core):** Tracing decorators (`@trace_tool_call`, `@trace_llm_call`), OTLP batching exporter.
* **Collector Service:** OTLP gRPC ingestion, payload validation, and ClickHouse batch writing.
* **Query API (REST):** Initial `GET /v1/traces` endpoints with project-scoped API key authentication.

### Milestone 3: Agent Runtime (Framework Integrations)
* **Common Integration Layer:** Built `agentmesh-integrations-common` to standardize span tracking, testing, and parent-child linkage across all event-driven frameworks.
* **Framework Adapters:** Fully instrumented adapters for **LangGraph**, **CrewAI**, **OpenAI Agents SDK**, and **AutoGen**, all emitting the exact same 4-kind span vocabulary (`llm.call`, `tool.call`, `agent.handoff`, `mcp.call`).
* **Golden Reference Tasks:** Established a shared, deterministic mock workflow (`search` -> `read_page` -> `summarize` -> `review`) that produces comparable trace DAGs regardless of the orchestration framework.
* **GraphQL Endpoint:** Added a native GraphQL surface to the Query API to fetch deeply nested trace trees efficiently.

### Milestone 4: Memory System & MVP Ship Line
* **Cost Engine v0.1:** Added static pricing definitions to the Go Collector, calculating and persisting `cost_usd` for LLM spans at ingestion time.
* **Trace Rollups:** Verified `trace_rollups` materialized view in ClickHouse, ensuring fast, aggregated dashboard queries without raw span scans.
* **Web Console v0.1:** The React/TypeScript dashboard (`web/console`) is fully scaffolded, successfully compiling with `oxlint` integration. Features trace DAG viewer, trace list, and basic cost dashboard.
* **Retention/Compaction Job:** Added `services/jobs`, a Postgres-backed background queue utilizing `SKIP LOCKED` polling to execute table maintenance (e.g. `ALTER TABLE spans DELETE` driven by per-project `retention_days`).
* **Engineering Excellence (CI/CD):** Implemented GitHub Actions CI/CD workflows covering Go, Python, and Web codebases, creating a secure gateway before merging to `main`.
* **SDK Polish:** Enhanced the `SpanTracker` interface (`set_input()`), empowering deeper introspection into tool calls mid-flight.

### Milestone 5: Terminal Experience
* **Realtime Gateway:** New Go service (`services/realtime-gateway`) bridging Collector-published Redis pub/sub span events to live WebSocket sessions, with lazy per-project subscription lifecycle (only pays for a Redis subscription while a client is actually listening).
* **Collector Realtime Publish:** Collector now publishes a lightweight span event to Redis immediately after a successful ClickHouse write — best-effort, never blocking or failing ingestion if Redis is unavailable.
* **`agentmesh` CLI:** New Go CLI (Cobra + Bubble Tea) with `agentmesh tail --project <id>` (live-streaming terminal view of spans as they arrive) and `agentmesh mcp validate <manifest.yaml>` (structural linter for MCP server registration manifests, built ahead of the Registry/Gateway itself so the same validation logic is reused in Milestone 6).
* **CLI Distribution:** GoReleaser config + GitHub Actions release workflow producing cross-platform (Linux/macOS/Windows, amd64/arm64) binaries and a Homebrew formula on `cli-v*` tags.
* **Deployment Completeness:** Wired both `realtime-gateway` and the previously-orphaned `jobs` worker (Milestone 4) into `docker-compose.yml` with Dockerfiles, and fixed a broken CI job (`go test ./...` at the repo root silently failed under the multi-module `go.work` layout) by matrixing Go tests per module.

### Milestone 6: Plugin/MCP Ecosystem
* **MCP Registry:** New Postgres schema (`mcp_servers`, `guardrail_policies`, `mcp_server_tokens`) plus a full Query API REST CRUD surface (`services/query-api/internal/rest/mcp_registry.go`) and a new Web Console **Registry view** — list registered servers, mint per-caller bearer tokens, remove servers — all backed by real, live-verified endpoints.
* **MCP Gateway Multi-Server Routing:** The Gateway no longer proxies to one hardcoded upstream; it resolves `{server_name}` against the Registry per request (`services/mcp-gateway/internal/registry`), with the legacy single-upstream mode kept working unchanged for backward compatibility.
* **OAuth 2.1-Style Caller Auth:** Per-server opaque bearer tokens (`services/mcp-gateway/internal/oauth`), hashed at rest via the same convention as AgentMesh's own API keys — deliberately scoped to bearer-token validation rather than a full authorization-code+PKCE flow, since that doesn't fit MCP's machine-to-machine calling pattern (matches the protocol's own 2026 client-credentials direction).
* **Rate Limiting:** Redis fixed-window limiter (`services/mcp-gateway/internal/ratelimit`) enforced per `(server, caller)`, driven by an additive `rate_limit:` field on the existing guardrail policy YAML DSL — zero breaking changes to the pre-existing policy engine or its tests.
* **Mock CRM MCP Server:** A minimal stdlib-only example target (`examples/mock-crm-mcp-server`) satisfying the milestone's exact success-criteria demo.
* **`agentmesh mcp register`:** New CLI command completing the SDK→Registry→Gateway loop end to end.
* **Two real bugs found and fixed during live verification** (not just unit tests): the Query API returned `{"traces":null}` instead of `[]` for an empty ClickHouse result, crashing the Console's default view for any brand-new project; and the CORS middleware was missing `DELETE` from `Access-Control-Allow-Methods`, silently blocking the new server-removal endpoint from any browser. Both fixed and re-verified live.

### Additional Completed Work
* **Marketing Site:** Built a responsive, polished landing page (React, Tailwind v4, Framer Motion) communicating the product vision, linking directly to the repository and documentation.

---
### Milestone 7: AI Workflows (Replay & Anomalies)
* **Deterministic Replay Engine:** Implemented the Python SDK replay shim (`AGENTMESH_REPLAY_ID`) and `Golden Trace Regression Suite` (`tests/replay_regression`).
* **Anomaly Detector:** Streaming detection of infinite loops, cost spikes, and guardrail violations (`services/anomaly-detector`), listening to the Redis span event stream and populating `alert_events` in Postgres.
* **Alerting Service:** `SKIP LOCKED` Postgres queue worker sending notifications (e.g., Slack webhooks) for detected anomalies (`services/alerting-service`).

### Milestone 8: Polish & Optimization
* **Helm Charts:** Created production Kubernetes manifests (`deploy/helm/agentmesh`) for stateless services (`collector`, `query-api`, `mcp-gateway`, `anomaly-detector`, `alerting-service`, `realtime-gateway`, `jobs`).
* **Documentation & Optimization:** Expanded `README.md` with Quick Start, Architecture Overview, and Features. Validated critical ClickHouse and Postgres indexing. Fully cleaned codebase and cleared technical debt.
---

## Technical Health & Notes
* **Strict Span Model:** The core architecture enforces exactly 4 span kinds. All framework-specific details (like node types or memory operations) are safely encoded into the open-ended attributes dictionary, keeping the schema clean and performant.
* **Codebase Cleanliness:** `oxlint`, `pytest`, and `go test` suites are fully green. Test coverage includes edge cases for orphaned spans and parent-child linkage.