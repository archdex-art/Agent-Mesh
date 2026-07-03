# AgentMesh

A framework-agnostic control plane for AI agents: see every decision your agents make, replay any failure exactly, and govern every tool they call — without rewriting your agent stack.

AgentMesh is **not** another agent-building framework (LangGraph, CrewAI, AutoGen already solve that well) and **not** another coding assistant. It's infrastructure that any of those tools plug into, providing:

## Features

1. **Framework-agnostic execution tracing** via OpenTelemetry — capture the full DAG of an agent run regardless of what built it. Monitor token usage, latency, and agent-to-tool handoffs.
2. **Deterministic replay** — re-run any historical agent trace exactly, with recorded tool responses. A massive time-saver for debugging edge cases and testing prompts.
3. **MCP-native governance** — auth, audit trails, and guardrail policies for any Model Context Protocol server. Apply policies globally without touching your agents.
4. **Cost and anomaly intelligence** — per-agent, per-tool, per-user spend tracking with automatic loop/spike detection and threshold alerting.

## Architecture Overview

AgentMesh acts as a sidecar/control-plane to your agent workloads.

- **Stateless Services (Go)**:
  - `collector`: Ingests OTLP spans from SDKs, authenticates them, and writes to ClickHouse.
  - `query-api`: Serves trace data, cost metrics, and anomalies to the Web Console.
  - `mcp-gateway`: A proxy for MCP servers, enforcing guardrails and issuing OAuth 2.1-style tokens.
  - `anomaly-detector` & `alerting-service`: Analyzes live spans for loops/cost spikes and dispatches webhooks.
  - `replay-engine`: Re-runs traces by injecting historical tool outputs.
- **Data Tier**:
  - **Postgres**: Control-plane state (projects, API keys, MCP registry, guardrails, alert rules).
  - **ClickHouse**: High-volume telemetry (spans, rollups).
  - **Redis**: Live span pub/sub for anomaly detection.
  - **MinIO**: Blob storage for large I/O payloads.

See [`docs/plan/Vision.md`](docs/plan/Vision.md) for the full product vision, [`docs/plan/Architecture.md`](docs/plan/Architecture.md) for the system design, and [`docs/otlp-mapping.md`](docs/otlp-mapping.md) for the wire contract between the SDKs and the Collector.

## Quick Start

Bring up the full stack locally using Docker Compose:

```sh
git clone https://github.com/agentmesh/agentmesh.git
cd agentmesh/deploy
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

## Project Status

**Milestones 1–8 — complete.** See [`docs/plan/Milestones.md`](docs/plan/Milestones.md) for the full roadmap.

* **Milestone 1 & 2**: Foundation, OTLP collection, and Query API.
* **Milestone 3**: Agent Framework adapters (LangGraph, CrewAI, AutoGen, OpenAI).
* **Milestone 4 & 5**: Web Console, UX, and Auth.
* **Milestone 6**: MCP Governance, Guardrails, and Proxy.
* **Milestone 7 & 8**: Replay Engine, Anomaly Detection, Alerting, Helm Charts, and Final Polish.


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

## Contributing

This project follows a monorepo-with-clear-boundaries structure (see `docs/plan/Repository Structure.md` for why). Each `services/*` directory owns its own `internal/` package; cross-service contracts live in `proto/`; shared data-model definitions live in `schema/`, `shared/span/`, and `docs/otlp-mapping.md`.
