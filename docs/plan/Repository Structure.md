# AgentMesh — Repository Structure

AgentMesh ships as a single monorepo. At this team size (solo-to-small), a monorepo with clear internal module boundaries gives atomic cross-service commits (e.g., changing the span schema and updating every consumer in one PR) without the coordination overhead of multi-repo versioning — a tradeoff revisited only if the project grows enough contributors that CI time or ownership boundaries demand splitting (see `Risks.md`, Maintenance Risks).

```
agentmesh/
├── services/                   # Independently deployable Go services
│   ├── collector/               # OTLP ingestion -> ClickHouse/blob store, single responsibility: get spans in durably
│   │   ├── cmd/
│   │   ├── internal/
│   │   │   ├── ingest/           # OTLP gRPC/HTTP receiver + validation
│   │   │   ├── writer/           # ClickHouse batch writer
│   │   │   └── blobstore/        # S3-compatible client for large payloads
│   │   └── Dockerfile
│   ├── query-api/                # REST + GraphQL surface for Console/CLI, single responsibility: answer read queries
│   │   ├── cmd/
│   │   ├── internal/
│   │   │   ├── rest/
│   │   │   ├── graphql/
│   │   │   └── authz/            # RBAC/API-key checks shared by both REST and GraphQL handlers
│   │   └── Dockerfile
│   ├── realtime-gateway/         # WebSocket/SSE fan-out, single responsibility: push live span events
│   ├── mcp-gateway/               # MCP reverse proxy, single responsibility: enforce auth/guardrails on tool calls
│   │   ├── internal/
│   │   │   ├── proxy/
│   │   │   ├── policy/            # guardrail DSL evaluator
│   │   │   └── ratelimit/
│   ├── replay-engine/              # Trace reconstruction + execution replay, single responsibility: reproduce a trace
│   │   ├── internal/
│   │   │   ├── trajectory/         # read-only replay mode
│   │   │   └── execution/          # interactive replay mode (talks to the SDK's replay shim)
│   ├── anomaly-detector/            # Streaming rule evaluation, single responsibility: flag bad trajectories/cost spikes
│   ├── cost-engine/                  # Token/dollar attribution, single responsibility: compute and roll up cost
│   ├── auth-service/                  # API keys + (post-MVP) OIDC sessions, single responsibility: identity
│   └── alerting-service/               # Outbound Slack/PagerDuty/webhook delivery, single responsibility: notify
│
├── sdk/
│   ├── python/                    # `agentmesh-sdk` PyPI package
│   │   ├── agentmesh/
│   │   │   ├── tracer.py            # core span-capture API
│   │   │   ├── exporter.py          # OTLP batching/export
│   │   │   └── replay_shim.py       # intercepts tool calls when AGENTMESH_REPLAY_ID is set
│   │   └── tests/
│   ├── typescript/                 # `@agentmesh/sdk` npm package
│   │   ├── src/
│   │   └── tests/
│   └── integrations/                # Framework-specific adapters, each independently versioned
│       ├── langgraph/
│       ├── crewai/
│       ├── autogen/
│       └── openai-agents-sdk/
│
├── cli/                             # `agentmesh` Go CLI, single responsibility: local developer workflows
│   ├── cmd/
│   │   ├── tail.go
│   │   ├── replay.go
│   │   └── mcp.go
│   └── internal/tui/                # Bubble Tea views
│
├── web/
│   └── console/                     # React/TypeScript Web Console
│       ├── src/
│       │   ├── features/
│       │   │   ├── traces/            # trace list + DAG viewer
│       │   │   ├── cost/              # cost dashboards
│       │   │   ├── replay/            # replay UI
│       │   │   ├── registry/          # MCP server registry management
│       │   │   └── alerts/            # alert rule configuration
│       │   ├── api/                   # generated Query API client (REST + GraphQL)
│       │   └── components/            # shared UI primitives
│       └── tests/
│
├── proto/                           # Shared protobuf/OTLP schema extensions and internal gRPC contracts
│   └── agentmesh/v1/
│
├── schema/                          # Source-of-truth data definitions consumed by multiple services
│   ├── clickhouse/                  # .sql migration files for the spans table + materialized views
│   └── postgres/                    # migration files for the control-plane schema
│
├── deploy/
│   ├── docker-compose.yml           # Local/self-host single-node profile
│   ├── helm/                        # Production Kubernetes chart
│   └── terraform/                   # (post-MVP) hosted-cloud infrastructure-as-code
│
├── docs/                            # This planning corpus + future user-facing documentation
│   └── plan/                        # (this directory) — Vision, PRD, Architecture, etc.
│
├── examples/                        # Reference agent apps used for demos, integration tests, and the golden-trace suite
│   ├── langgraph-support-bot/
│   ├── crewai-research-crew/
│   ├── autogen-debate/
│   └── openai-agents-sdk-handoff-demo/
│
├── Makefile                         # Cross-service build/test/lint orchestration
└── README.md
```

## Rationale by Directory

- **`services/`** — one folder per deployable Go service, each with its own `Dockerfile` and `internal/` package so no service imports another service's internals; the only sharing is through `proto/` (contracts) and `schema/` (data definitions). This directly enforces the "service boundaries drawn along data ownership" rule from `Architecture.md` §2.
- **`sdk/`** — split by language at the top level (`python/`, `typescript/`) and then a separate `integrations/` folder for framework adapters, because adapters are versioned and released independently of the core SDK (Feature Roadmap's "framework reference integrations" entry) — a LangGraph API change should never force a core SDK release.
- **`cli/`** — kept separate from `services/` even though it's also a Go binary, because it has a fundamentally different deployment model (distributed as a binary to developer machines, not run as a server) and a different dependency graph (it depends on the public Query API as a client, never on internal service packages directly).
- **`web/console/`** — organized by feature (`features/traces`, `features/cost`, etc.) rather than by technical layer (`components/`, `hooks/`, `pages/`), because each feature maps 1:1 to a Query API capability and to a Feature Roadmap entry, making it easy to find "where does the replay UI live" without cross-referencing multiple top-level folders.
- **`proto/`** — a single shared source of truth for cross-service contracts prevents the classic monorepo failure mode of two services silently drifting on what a message field means; every service's build regenerates client code from here.
- **`schema/`** — migrations for both databases live outside any single service's folder because both ClickHouse and Postgres schemas are consumed by more than one service (e.g., both the Collector and the Replay Engine read the `spans` table); putting migrations in, say, `services/collector/` would incorrectly imply Collector ownership.
- **`deploy/`** — separates the three deployment targets (`docker-compose.yml` for local/self-host, `helm/` for production Kubernetes, `terraform/` for the future hosted tier) so each can evolve independently; `terraform/` starts as an empty placeholder and is populated only at Milestone 8, per `Product Requirements.md` §7's explicit MVP scope boundary.
- **`examples/`** — reference agent apps serve three purposes at once (developer-facing demos, integration test fixtures for the framework adapters, and the golden-trace corpus for Replay Engine regression testing per `Technical Roadmap.md` §7), justifying a single shared location rather than duplicating similar demo apps across `sdk/integrations/*/tests/`.
- **`docs/`** — houses this planning corpus (`docs/plan/`) today and will house user-facing documentation (getting-started guides, API reference) once the product ships; keeping planning and future user docs under the same top-level `docs/` avoids a later "where do docs live" reorganization.
