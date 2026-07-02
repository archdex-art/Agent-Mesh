# AgentMesh Project Report

**Date:** July 2026  
**Status:** MVP Launched (Milestone 4 of 8 Complete)

## Executive Summary
AgentMesh is a framework-agnostic control plane for AI agents that provides execution tracing, deterministic replay, MCP-native governance, and cost intelligence. 
The project has successfully laid its foundational data models, established the telemetry ingestion pipeline, completed reference integrations for the top four agent frameworks, and crossed the MVP ship line by delivering the Memory System and Web Console.

---
## 🟢 Completed Work (Milestones 1–4)

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

### Additional Completed Work
* **Marketing Site:** Built a responsive, polished landing page (React, Tailwind v4, Framer Motion) communicating the product vision, linking directly to the repository and documentation.

---

## 🟡 Immediate Next Step: Milestone 5

**Milestone 5 — Terminal Experience**
* `agentmesh tail` CLI for live-streaming spans via a new **Realtime Gateway** (Redis pub/sub -> WebSocket fan-out).
* CLI-based MCP server manifest validation.

### Milestone 6: Plugin / MCP Ecosystem
* **MCP Registry:** Postgres-backed catalog of approved MCP servers.
* **MCP Gateway:** A reverse proxy adding OAuth 2.1 authentication, audit trails, and declarative guardrail policies (rate limits, allow/deny lists) to any standard MCP server.

### Milestone 7: AI Workflows (Replay & Anomalies)
* **Deterministic Replay Engine:** Re-run historical agent traces using recorded tool I/O (trajectory mode for read-only viewing, execution mode for live testing against patched agent code).
* **Anomaly Detector:** Streaming detection of infinite loops, cost spikes, and guardrail violations.
* **Alerting Service:** Push notifications to Slack and PagerDuty.

### Milestone 8: Polish & Optimization
* ClickHouse query performance tuning.
* Formal load testing of the ingestion path.
* Comprehensive public documentation and Helm charts for production Kubernetes deployment.

---

## Technical Health & Notes
* **Strict Span Model:** The core architecture enforces exactly 4 span kinds. All framework-specific details (like node types or memory operations) are safely encoded into the open-ended attributes dictionary, keeping the schema clean and performant.
* **Codebase Cleanliness:** `oxlint`, `pytest`, and `go test` suites are fully green. Test coverage includes edge cases for orphaned spans and parent-child linkage.