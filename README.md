# AgentMesh

A framework-agnostic control plane for AI agents: see every decision your agents make, replay any failure exactly, and govern every tool they call — without rewriting your agent stack.

AgentMesh is **not** another agent-building framework (LangGraph, CrewAI, AutoGen already solve that well) and **not** another coding assistant. It's infrastructure that any of those tools plug into, providing:

1. **Framework-agnostic execution tracing** via OpenTelemetry — capture the full DAG of an agent run regardless of what built it.
2. **Deterministic replay** — re-run any historical agent trace exactly, with recorded tool responses.
3. **MCP-native governance** — auth, audit trails, and guardrail policies for any Model Context Protocol server.
4. **Cost and anomaly intelligence** — per-agent, per-tool, per-user spend tracking with automatic loop/spike detection.

See [`docs/plan/Vision.md`](docs/plan/Vision.md) for the full product vision, [`docs/plan/Architecture.md`](docs/plan/Architecture.md) for the system design, and [`docs/otlp-mapping.md`](docs/otlp-mapping.md) for the wire contract between the SDKs and the Collector.

## Project Status

**Milestones 1–2 — complete.** See [`docs/plan/Milestones.md`](docs/plan/Milestones.md) for the full roadmap.

**Milestone 1 (Foundation):**
- [x] Monorepo scaffold
- [x] Shared Go packages (`ids`, `errors`, `logging`, `config`, `span`, `authkeys`) — the domain model and cross-cutting primitives every service depends on
- [x] Versioned ClickHouse span schema + `trace_rollups` materialized view
- [x] Postgres control-plane schema (projects, API keys)
- [x] `docker-compose.yml` local dev environment with health checks
- [x] CI skeleton (Go lint/test, schema migration verification)

**Milestone 2 (Core Engine):**
- [x] Python SDK (`agentmesh-sdk`): `@trace_llm_call`/`@trace_tool_call` decorators, batching OTLP exporter
- [x] Collector service: OTLP gRPC ingestion, API-key authentication, ClickHouse batch writer
- [x] Query API v0.1: `GET /v1/traces`, `GET /v1/traces/{id}` (REST), API-key middleware
- [x] Both services containerized (distroless multi-stage builds) and wired into `docker-compose.yml`

Verified end-to-end: a Python agent instrumented with the SDK's decorators produces a multi-span trace (with correct parent/child linkage) that is authenticated, persisted to ClickHouse, and retrievable via the Query API — using the actual production Docker images running against the compose stack, not a mocked or `go run` shortcut.

**Next: Milestone 3 (Agent Runtime)** — reference integrations for LangGraph, CrewAI, AutoGen, and the OpenAI Agents SDK.

## Repository Layout

See [`docs/plan/Repository Structure.md`](docs/plan/Repository%20Structure.md) for the full rationale. Summary:

```
services/       Independently deployable Go services (collector, query-api, mcp-gateway, ...)
sdk/            Python + TypeScript instrumentation SDKs, plus framework adapters
cli/            The `agentmesh` Go CLI
web/console/    React/TypeScript Web Console
web/marketing/  Public marketing site (React + Framer Motion + Tailwind)
proto/          Shared protobuf/gRPC contracts across services
schema/         ClickHouse + Postgres migrations (source of truth for the data model)
deploy/         docker-compose (local/self-host), Helm (production), Terraform (hosted, post-MVP)
shared/         Cross-service Go packages: ids, errors, logging, config, span, authkeys
examples/       Reference agent apps used as demos, integration fixtures, and the replay test corpus
docs/           otlp-mapping.md (SDK-to-Collector wire contract) + docs/plan/ (the planning corpus)
```

## Local Development

Bring up the full stack (Postgres, ClickHouse, Redis, MinIO, Collector, Query API):

```sh
cd deploy
docker compose -p agentmesh up -d --build
docker compose -p agentmesh ps   # wait for the storage services to report "healthy"
```

Default host ports avoid common collisions (Postgres on `15432`, Redis on `16379`); override via `AGENTMESH_POSTGRES_PORT` / `AGENTMESH_REDIS_PORT` / `AGENTMESH_COLLECTOR_PORT` / `AGENTMESH_QUERYAPI_PORT` env vars if those are also taken.

Migrations under `schema/postgres/` and `schema/clickhouse/` apply automatically on first container start via each image's init-script mechanism.

### Instrumenting an agent with the Python SDK

```sh
pip install -e sdk/python  # once published: pip install agentmesh-sdk
```

```python
import agentmesh

tracer = agentmesh.configure(
    project_id="<your-project-uuid>",
    api_key="am_live_...",
    endpoint="localhost:4317",
)

@agentmesh.trace_tool_call(name="web_search")
def search(query: str) -> str:
    ...

@agentmesh.trace_llm_call(name="gpt-4.1")
def call_model(prompt: str) -> str:
    ...

with tracer.start_span(agentmesh.SpanKind.AGENT_HANDOFF, "my-agent"):
    result = search("...")
    answer = call_model(result)

tracer.shutdown()
```

Then query the trace back:

```sh
curl -H "X-AgentMesh-API-Key: am_live_..." http://localhost:8080/v1/traces
```

### Running tests

```sh
make test              # unit tests for every Go module (no live infra needed)
make test-integration  # ClickHouse/Postgres-backed tests (requires `make up` first)
cd sdk/python && python -m pytest tests/ -v
```

### Marketing site

```sh
cd web/marketing
npm install
npm run dev      # local dev at http://localhost:5173
npm run build    # production build (tsc -b && vite build)
```

Or via Docker (optional compose profile, not part of the core stack):

```sh
cd deploy
docker compose -p agentmesh --profile marketing up -d --build marketing
# serves at http://localhost:8888
```

## Contributing

This project follows a monorepo-with-clear-boundaries structure (see `docs/plan/Repository Structure.md` for why). Each `services/*` directory owns its own `internal/` package; cross-service contracts live in `proto/`; shared data-model definitions live in `schema/`, `shared/span/`, and `docs/otlp-mapping.md`.
