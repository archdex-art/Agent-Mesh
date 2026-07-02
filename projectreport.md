# AgentMesh Project Report

**Date:** July 2026  
**Status:** Approaching MVP (Milestone 3 of 8 Complete)

## Executive Summary
AgentMesh is a framework-agnostic control plane for AI agents that provides execution tracing, deterministic replay, MCP-native governance, and cost intelligence. 

The project has successfully laid its foundational data models, established the telemetry ingestion pipeline, and completed reference integrations for the top four agent frameworks. We are currently preparing to cross the MVP ship line with the build-out of the Web Console and Memory System.

---

## 🟢 Completed Work (Milestones 1–3)

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

### Additional Completed Work
* **Marketing Site:** Built a responsive, polished landing page (React, Tailwind v4, Framer Motion) communicating the product vision, linking directly to the repository and documentation.

---

## 🟡 Immediate Next Step: Milestone 4 (MVP Ship Line)

**Milestone 4 — Memory System (Trace Data Lifecycle)**
This milestone transforms the raw data pipeline into a usable product. Once complete, a design partner can self-host AgentMesh and debug traces via the UI.
* **Cost Engine v0.1:** Span-level token and dollar cost attribution via a static pricing table.
* **Trace Rollups:** A ClickHouse materialized view (`trace_rollups`) to pre-aggregate trace costs and durations for fast querying.
* **Web Console v0.1:** A React/TypeScript dashboard featuring a trace list, DAG viewer, and basic cost dashboard.
* **Retention/Compaction Job:** A Postgres-backed worker to enforce data lifecycle policies (ClickHouse TTL + blob storage cleanup).

---

## ⚪ Upcoming Roadmap (Post-MVP)

### Milestone 5: Terminal Experience
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