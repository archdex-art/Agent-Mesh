# AgentMesh — Technical Roadmap (Technology Decisions)

Each decision below states the alternatives considered and why the chosen option won, per the working principle "explain the reasoning behind every major architectural decision."

## 1. Programming Languages

| Component | Language | Alternatives considered | Justification |
|---|---|---|---|
| Collector, Query API, Realtime Gateway, MCP Gateway, Anomaly Detector, Cost Engine | **Go** | Rust, Node.js | Go gives near-C performance for I/O-bound proxy/pipeline services with a much smaller learning curve and faster iteration than Rust, and stronger concurrency primitives (goroutines/channels) than Node for a service that's fundamentally about fanning out streams. Every reference architecture researched for this space (Collector-style services) favors Go or Rust; Go is chosen here to keep a solo/small-team build velocity high without sacrificing the performance the ingestion path needs. |
| Python SDK | **Python** | n/a (required) | LangGraph, CrewAI, AutoGen, and the majority of custom agent loops are Python-first; this SDK is not a stylistic choice but a market requirement, shipped first (Milestone 2) ahead of the TypeScript SDK. |
| TypeScript SDK, Web Console | **TypeScript** | n/a | TS is required for the OpenAI Agents SDK's TypeScript support and any Node-based custom agent; React (below) requires TS for the Console regardless. |
| CLI | **Go** | Python, Rust | A single static binary with no runtime dependency is essential for a tool developers install casually; Go's cross-compilation story (`GOOS`/`GOARCH`) makes multi-platform CLI distribution trivial compared to Python (interpreter/dependency management) or Rust (longer build iteration for a small team). |

## 2. Frameworks

| Purpose | Choice | Alternatives considered | Justification |
|---|---|---|---|
| Web Console | **React + TypeScript**, Vite build, TanStack Query for data fetching | Vue, Svelte | React's ecosystem has the most mature options for the two hardest UI problems here — trace DAG visualization (React Flow / D3 integration) and real-time data (React Query + WebSocket patterns are well-trodden). Team familiarity and hiring pool also favor React for a project meant to be portfolio-legible to other engineers. |
| Trace DAG visualization | **React Flow** (or D3.js if React Flow's layout model proves too rigid for deeply nested handoff graphs) | Cytoscape.js | React Flow integrates natively with the React component model already chosen for the Console, minimizing glue code; D3 remains the fallback if node-graph layout requirements outgrow React Flow's capabilities (evaluated at Milestone 8 polish, not committed upfront). |
| Terminal UI | **Bubble Tea** (Go) | Ratatui (Rust) | Consistent with the CLI's Go choice above; Bubble Tea has strong ergonomics for the tail/replay views needed. |
| gRPC/OTLP | **OpenTelemetry Go SDK / Python SDK / TS SDK**, standard OTLP protobuf definitions | Custom binary protocol | Building a custom wire format would forfeit the single biggest strategic advantage identified in `Vision.md` — interoperability with anything already OTel-instrumented. Not a real alternative once the "protocol-native" principle (`Product Requirements.md` §2) is accepted. |

## 3. Runtime

- **Collector/Gateway/API services:** compiled Go binaries, no runtime dependency, deployed as minimal `scratch`/`distroless` container images.
- **Web Console:** static build served via any CDN/reverse proxy (no Node server required in production — Vite output is static assets).
- **SDKs:** run inside the customer's existing process/runtime (whatever Python or Node version their agent already uses) — AgentMesh does not control this runtime and must support a wide compatibility matrix (Python 3.9+, Node 18+) rather than requiring bleeding-edge versions.

## 4. Database

| Store | Choice | Alternatives considered | Justification |
|---|---|---|---|
| Trace/span storage | **ClickHouse** | TimescaleDB, PostgreSQL+pgvector, Elasticsearch | ClickHouse's column-oriented storage and MergeTree engine are purpose-built for append-heavy, time-partitioned, aggregation-query-heavy workloads — exactly the trace-store access pattern (write once, query by time range + aggregate cost/latency). TimescaleDB is a credible alternative and a lower-ops-overhead fallback if ClickHouse's operational complexity proves too high for a solo-maintained self-hosted product (documented as a Milestone 8 evaluation checkpoint, not a blocking decision now). |
| Control-plane metadata | **PostgreSQL** | MySQL, SQLite | Relational integrity for projects/keys/policies/registry entries with mature JSON column support (for the flexible `manifest_yaml`/`rule_dsl` fields) and the best operational familiarity across the ecosystem — the "boring infrastructure" principle applies directly here. |
| Pub/sub + rate limiting | **Redis** | NATS, Kafka | Redis is already a near-universal dependency in this space and is sufficient for the fan-out and counter workloads at MVP scale (`System Design.md` §5); Kafka is explicitly rejected at this stage as premature — its operational overhead is not justified until ingestion volume outgrows Redis pub/sub, which is a scale AgentMesh does not expect to hit before Milestone 8. |
| Blob storage | **S3-compatible (MinIO self-hosted / AWS S3 cloud)** | storing large payloads directly in ClickHouse/Postgres | Keeping large tool-I/O payloads out of the hot trace-query path (ClickHouse) keeps span-scan queries fast; S3-compatible storage is the standard answer and MinIO makes self-hosting trivial (single container, S3 API-compatible). |

## 5. AI SDKs / Model Access

AgentMesh itself only needs LLM access for two specific, deferred features (LLM-assisted trace summarization, guardrail-policy suggestion) — it is not an LLM-calling product at its core. When needed (Milestone 7+):

- **Provider-agnostic client** (e.g., a thin wrapper matching whichever providers customers already use — OpenAI, Anthropic, open-weight via a local inference server) rather than hard-coding a single vendor, consistent with the "no framework lock-in" philosophy applied to AgentMesh's own choices, not just its customers'.

## 6. Communication Protocols

Covered in depth in `Architecture.md` §9. Summary of choices: **OTLP/gRPC** (ingestion), **MCP** (Streamable HTTP + stdio, Gateway), **REST + GraphQL** (Query API), **WebSocket** (Realtime Gateway), **Redis pub/sub** (internal fan-out).

## 7. Testing Frameworks

| Layer | Tooling | Justification |
|---|---|---|
| Go services | standard `testing` package + `testify` for assertions, `testcontainers-go` for ClickHouse/Postgres/Redis integration tests | Avoids mocking the database layer for anything beyond pure unit logic — integration tests against real (containerized) ClickHouse are the only way to trust MergeTree/TTL behavior. |
| Python SDK | `pytest` + `pytest-recording`/`vcrpy`-style HTTP fixture capture for OTLP export tests | Standard, well-understood Python testing stack; fixture capture avoids needing a live Collector for every SDK unit test. |
| TypeScript SDK / Web Console | `vitest` + `@testing-library/react` for component tests, `playwright` for end-to-end Console flows (trace list → DAG view → replay trigger) | Vitest's speed and Vite-native config match the chosen build tool; Playwright is the current standard for realistic E2E browser testing and is needed to verify the Realtime Gateway's WebSocket push actually renders. |
| Replay Engine correctness | dedicated **golden-trace test suite**: a fixed set of recorded traces from the reference framework integrations (Milestone 3), replayed on every Replay Engine change, diffed against expected output | This is the highest-risk subsystem (`Risks.md`, Technical Risks); it gets its own regression suite rather than relying on generic unit tests. |

## 8. Build Tools

- **Go services:** standard `go build`, multi-stage Dockerfiles producing distroless final images.
- **Web Console:** Vite (fast dev server, native TS/React support, small production bundles).
- **Monorepo tooling:** a single top-level `Makefile`/`Taskfile` orchestrating per-service builds rather than adopting a heavyweight monorepo build system (Bazel/Nx) at this scale — revisit only if the number of services and cross-service build dependencies grows past what a solo/small team can reason about with simple scripts (see `Repository Structure.md`).

## 9. CI/CD

- **CI:** GitHub Actions — per-service jobs (lint, unit test, integration test via `testcontainers`) triggered on PR, plus a golden-trace replay regression job gating any change under `services/replay/`.
- **CD:** container images built and pushed to a container registry (GHCR) on merge to `main`; a Helm chart (see below) is the deployment artifact for production Kubernetes installs, and a versioned `docker-compose.yml` is the artifact for self-hosted single-node installs.
- **Release versioning:** semantic versioning across the whole stack (`v0.1.0`, etc.) with the trace-span schema version tracked *separately* (`schema_version` field on every span) so a Collector/SDK version mismatch during a rolling upgrade never produces silently malformed data.

## 10. Packaging

- **SDKs:** published to PyPI (`agentmesh-sdk`) and npm (`@agentmesh/sdk`) following each ecosystem's normal conventions.
- **Services:** OCI container images, one per service, versioned and tagged consistently.
- **CLI:** distributed as prebuilt binaries per platform (GitHub Releases) plus a Homebrew formula, avoiding any requirement to install Go to use the CLI.

## 11. Deployment Strategy

| Environment | Mechanism | Notes |
|---|---|---|
| Local dev / evaluation | `docker-compose.yml` bringing up Postgres, ClickHouse, Redis, MinIO, Collector, Query API, Realtime Gateway, Web Console in one command | This is the primary onboarding path validated against Assumption A4 (`Vision.md`); must work on a clean machine in under 10 minutes. |
| Self-hosted production | Helm chart for Kubernetes, with a documented single-VM `docker-compose` production profile as a lighter-weight alternative for smaller teams | Mirrors the Langfuse adoption pattern identified in competitive research — self-hosting must not require a Kubernetes expert to get started. |
| Hosted (post-MVP, Milestone 8+) | Managed multi-tenant deployment (cloud provider TBD at that stage) with per-project isolation enforced at the ClickHouse/Postgres row level (`project_id` on every table, enforced in the Query API's data-access layer) | Explicitly out of scope for MVP engineering effort; the schema design already supports it (every table is `project_id`-scoped from day one) so this is additive infrastructure work later, not a redesign. |
