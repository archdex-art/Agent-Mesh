# AgentMesh — Development Milestones

Estimates assume a single senior engineer working part-time (~15–20 hrs/week), consistent with the freelance-portfolio context this project originates from. Total MVP-to-launch estimate: **~20–24 weeks** across all 8 milestones (M1–M4 constitute the MVP, ~10 weeks; M5–M8 are the post-MVP build-out, ~10–14 weeks).

## Milestone 1 — Foundation

**Goals:** establish the monorepo, the span data model, and a working local dev environment — nothing user-facing yet, but everything downstream depends on getting the schema right once.

**Deliverables:**
- Monorepo scaffold per `Repository Structure.md`.
- Versioned span schema (`schema_version` field, `schema/clickhouse/` migrations) implementing the model in `System Design.md` §2.1.
- Postgres control-plane schema (`schema/postgres/`) for projects and API keys only (registry/policies/alerts added in later milestones).
- `docker-compose.yml` bringing up Postgres, ClickHouse, Redis, MinIO with health checks.
- CI skeleton (lint + unit test jobs per `Technical Roadmap.md` §9).

**Risks:** getting the ClickHouse schema wrong (partition key, sort order) is expensive to fix after real data lands — mitigated by writing the golden-trace fixtures (from `examples/`) before any service code, so the schema is validated against realistic data shapes from day one, not designed in the abstract.

**Dependencies:** none (first milestone).

**Success criteria:** `docker compose up` produces a healthy stack; a manually-inserted test span round-trips through a raw ClickHouse query.

---

## Milestone 2 — Core Engine

**Goals:** the ingestion pipeline works end-to-end: a real Python agent can emit a trace that lands in ClickHouse and is visible via a query.

**Deliverables:**
- Python SDK v0.1 (`sdk/python/`): tracer + OTLP exporter, manual wrapping API (`@agentmesh.trace_llm_call`, `@agentmesh.trace_tool_call` decorators).
- Collector service: OTLP gRPC receiver, ClickHouse batch writer, blob-store writer for large payloads.
- Query API v0.1: `GET /v1/traces`, `GET /v1/traces/{id}` (REST only; GraphQL deferred to Milestone 3 when the Console needs it).
- API-key issuance and validation (Auth Service, minimal — just key CRUD + hashing).

**Risks:** ClickHouse batch-insert tuning (batch size vs. latency) is a genuine unknown until tested under load — mitigated by load-testing with a synthetic span generator before declaring this milestone done, not after.

**Dependencies:** Milestone 1 schema.

**Success criteria:** an example agent (`examples/langgraph-support-bot`, built as part of this milestone as the first integration test fixture) produces a trace that appears in a `GET /v1/traces` response within 2 seconds of the agent run completing.

---

## Milestone 3 — Agent Runtime (Framework Integrations)

**Goals:** prove the "framework-agnostic" claim with real, working adapters — and in doing so, validate Assumption A1 and A2 from `Vision.md`.

**Deliverables:**
- Reference integrations for LangGraph, CrewAI, AutoGen, and the OpenAI Agents SDK (`sdk/integrations/`).
- Four example agent apps (`examples/`), each instrumented with its respective integration, each performing a comparable multi-step task (e.g., "research a topic and summarize it using two tools") so their traces are directly comparable in the Console.
- GraphQL endpoint on the Query API (needed for the Console's nested trace-DAG queries, built in Milestone 4).

**Risks:** each framework's internal callback/middleware system is different enough that a truly uniform adapter interface may not be achievable without compromise — explicitly time-boxed: if any single integration exceeds 1.5x its estimated effort, it ships with documented limitations rather than blocking the milestone (a real risk flagged in `Risks.md`, Technical Risks).

**Dependencies:** Milestone 2's SDK and Collector.

**Success criteria:** all four example agents produce traces in AgentMesh with correctly-mapped `agent.handoff` spans (the hardest concept to map, per `Architecture.md` §3's mapping table); a human reviewer can look at any of the four traces and identify the same logical steps without knowing which framework produced it.

---

## Milestone 4 — Memory System (Trace Data Lifecycle)

**Goals:** move from "traces exist" to "traces are production-viable" — retention, compaction, and the Web Console's first real UI.

**Deliverables:**
- Retention/compaction background job (Postgres-backed job queue per `Architecture.md` §8) enforcing `projects.retention_days` via ClickHouse TTL and blob-store lifecycle rules.
- Web Console v0.1: trace list, trace DAG viewer, basic cost dashboard (the three P0 UI features from `Feature Roadmap.md`).
- `trace_rollups` materialized view for dashboard query performance.
- Cost Engine v0.1: static pricing table, per-span cost computation.

**Risks:** none rated above Medium — this milestone is largely additive engineering on a now-stable foundation. The main watch item is Console scope creep (see `Risks.md`, Product Risks) — the temptation to build replay UI early must be resisted since the Replay Engine doesn't exist yet.

**Dependencies:** Milestones 1–3.

**Success criteria: this is the MVP ship line.** A design partner (or the developer acting as their own first user) can self-host AgentMesh, instrument a real agent with the Python SDK, and answer "what did this agent do and what did it cost" entirely through the Web Console, with zero direct database queries.

---

## Milestone 5 — Terminal Experience

**Goals:** ship the `agentmesh` CLI as a first-class client, targeting the "Terminal Applications" differentiation identified in the original portfolio strategy.

**Deliverables:**
- `agentmesh tail` — live TUI streaming spans via the Realtime Gateway (built in this milestone, since nothing before it needed real-time push).
- `agentmesh mcp validate` — manifest linter (ahead of the Gateway itself, so the validation logic can be reused by the Gateway's registration flow in Milestone 6).
- CLI distribution: GitHub Releases binaries + Homebrew formula.

**Risks:** Realtime Gateway is new infrastructure (Redis pub/sub → WebSocket) — mitigated by building `tail` against a small number of concurrent sessions first and load-testing fan-out only if/when a design partner's usage demands it (deferred scaling, per `Technical Roadmap.md` §4's Redis justification).

**Dependencies:** Milestone 4 (needs a stable trace/span model to tail).

**Success criteria:** a developer can run `agentmesh tail --project demo` in one terminal, trigger the example LangGraph agent in another, and watch spans stream in within ~1 second of each tool call.

---

## Milestone 6 — Plugin/MCP Ecosystem

**Goals:** ship the MCP Gateway and Registry — the second pillar of AgentMesh's differentiation, and architecturally independent of everything built so far.

**Deliverables:**
- MCP Registry (Postgres schema + Query API/Console CRUD for server manifests).
- MCP Gateway: OAuth 2.1 validation, request forwarding, `mcp.call` span emission back to the Collector.
- Guardrail policy engine v1 (declarative YAML/JSON DSL): rate limits and allow/deny lists only for v1; the WASM-sandboxed custom-policy Innovative feature is explicitly deferred (`Feature Roadmap.md`).
- `agentmesh mcp register` CLI command.

**Risks:** OAuth 2.1 implementation correctness is security-critical and the single highest-stakes piece of code in the whole project — mitigated by using a well-audited OAuth library rather than hand-rolling token validation, and by the Gateway's fail-closed design (`Architecture.md` §17) limiting the blast radius of any auth bug to "requests denied," never "requests wrongly allowed" as the default failure mode.

**Dependencies:** Auth Service (minimal version exists from Milestone 2; extended here for OAuth 2.1 caller validation, distinct from AgentMesh's own API-key auth).

**Success criteria:** an example MCP server (a simple mock CRM tool built for this milestone) is registered, an agent calls it through the Gateway URL instead of directly, and the Console's Registry view shows the call logged with caller identity and latency; a deliberately malformed/unauthorized call is rejected and logged as `status=denied`.

---

## Milestone 7 — AI Workflows (Replay & Anomaly Detection)

**Goals:** ship the hardest and most differentiated subsystem — deterministic replay — plus the anomaly detection that makes AgentMesh proactive rather than purely reactive.

**Deliverables:**
- Trajectory-mode Replay Engine (read-only reconstruction/step-through).
- Execution-mode Replay Engine (SDK replay shim + interactive re-run against recorded tool I/O).
- Golden-trace regression test suite (`Technical Roadmap.md` §7) using the four example agents from Milestone 3 as the fixture corpus.
- Anomaly Detector v1: loop detection, cost-spike detection, guardrail-violation aggregation.
- Alerting Service: Slack + generic webhook delivery.
- LLM-assisted trace summarization (stretch goal within this milestone; can slip to Milestone 8 without blocking the rest).

**Risks:** this is the single highest-risk milestone in the plan (see `Risks.md`, Technical Risks — "replay determinism boundary"). Mitigation: ship trajectory-mode replay as a checkpoint before starting execution-mode, since trajectory mode alone validates most of the DAG-reconstruction logic without the added risk of the replay shim's interception correctness; if execution-mode replay proves harder than estimated, trajectory-mode replay is still a shippable, valuable milestone on its own.

**Dependencies:** Milestone 3's example agents (fixture corpus), Milestone 4's blob storage (large payload retrieval for replay).

**Success criteria:** for each of the four example agents, a developer can select a trace, click Replay, and get a byte-identical trajectory reconstruction (trajectory mode) and a working execution-mode re-run that produces the same tool-call sequence when the agent code is unchanged (execution mode) — this exact scenario is the golden-trace regression suite's core assertion, run in CI on every subsequent change.

---

## Milestone 8 — Polish & Optimization

**Goals:** the difference between "working" and "launchable" — performance, security hardening, documentation, and public-facing polish.

**Deliverables:**
- ClickHouse query performance pass (explain-analyze the Console's actual query patterns, add indexes/materialized views as needed).
- Load testing of the full ingestion path at a target volume (defined based on any design-partner data gathered by this point, or a documented synthetic target otherwise).
- Security review pass: dependency audit, secrets handling review, the MCP Gateway's auth path specifically re-reviewed given its Milestone 6 risk flag.
- Public documentation: getting-started guide, SDK API reference, self-hosting guide.
- Web Console UX polish: empty states, error states, onboarding flow.
- Helm chart for production Kubernetes deployment (the `docker-compose` self-host path already exists from Milestone 1; this milestone adds the production-grade alternative per `Technical Roadmap.md` §11).

**Risks:** "polish" milestones are notorious scope traps — mitigated by defining a fixed, written punch list at the *start* of this milestone (derived from design-partner feedback gathered during M1–M7, if any, or from the developer's own dogfooding) rather than open-ended iteration.

**Dependencies:** all prior milestones.

**Success criteria:** AgentMesh is publicly launchable — a stranger can find the GitHub repo, follow the README, have a working self-hosted instance with a traced example agent in under 15 minutes, and find the answer to "how do I get help" (docs/issues link) without asking anyone.
