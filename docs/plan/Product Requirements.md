# AgentMesh — Product Requirements

## 1. Mission Statement

> Give every team building AI agents the observability, reproducibility, and governance that production software has always required — without forcing them onto a specific agent framework.

## 2. Design Philosophy

1. **Protocol-native, not framework-native.** Every integration point is a standard (OpenTelemetry for traces, MCP for tools) rather than a proprietary SDK contract. If OpenTelemetry or MCP evolves, AgentMesh evolves with it instead of maintaining N bespoke framework adapters forever.
2. **Boring infrastructure, sharp edges only where they add value.** The trace store, auth, and API layer use proven, unglamorous technology (Postgres, ClickHouse, gRPC). Engineering effort concentrates on the two genuinely hard, differentiated problems: deterministic replay and MCP governance.
3. **Self-hosting is a first-class deployment target, not an afterthought.** A single `docker compose up` must produce a working instance. Enterprise features (SSO, multi-region, managed hosting) are additive, never required for core functionality.
4. **Traces are data, not logs.** Every span is structured, queryable, and exportable (Parquet/CSV) — a user's trace history is never trapped in a UI.
5. **No dark patterns around cost visibility.** Cost dashboards are accurate to the token, sourced from provider-reported usage where available, and never used to upsell rather than inform.

## 3. Core Principles

- **Zero required code changes to the agent's business logic** — instrumentation wraps or observes, it does not require restructuring control flow.
- **Every trace is replayable by default** once the SDK is integrated at the "record tool I/O" level; replay is not a separate opt-in instrumentation pass.
- **Every MCP server AgentMesh proxies keeps working exactly as before** if the Gateway is removed — the Gateway is additive middleware, not a required dependency of the server itself.
- **No silent data loss.** If the Collector cannot reach the Trace Store, spans buffer locally and retry; a crashed Collector must not silently drop an in-flight trace.
- **Consistent terminology across every surface** (CLI, dashboard, API, docs): *trace* (one full agent run), *span* (one step within a trace — an LLM call, tool call, or sub-agent handoff), *session* (one or more related traces, e.g., a multi-turn conversation).

## 4. User Personas (detail)

### 4.1 Agentic Product Engineer — "Priya"
- Builds a customer support agent on LangGraph at a 40-person startup.
- Ships to production weekly; owns the on-call rotation for agent failures.
- Pain: a support agent occasionally sends a wrong refund amount; by the time she's paged, the conversation state that caused it is gone.
- Success looks like: she pastes a trace ID into AgentMesh, hits "Replay," and steps through the exact tool calls that produced the bad output — in under 5 minutes.

### 4.2 Platform/DevEx Lead — "Marcus"
- Runs a platform team supporting six product teams, each using a different agent framework (two on CrewAI, one on AutoGen, three custom).
- Pain: no unified dashboard; each team built its own ad-hoc logging.
- Success looks like: he rolls out the AgentMesh SDK org-wide via a shared internal library; every team's traces land in one Trace Store with consistent cost attribution, without him having to standardize their frameworks first.

### 4.3 AI Consultant — "Elena" (freelancer)
- Hired for a 6-week engagement to harden a client's agent pipeline.
- Pain: unfamiliar codebase, no existing observability, client is skeptical of adding "yet another vendor."
- Success looks like: she self-hosts AgentMesh via Docker Compose in an afternoon, instruments the client's agent with the Python SDK, and delivers a trace-backed root-cause report by end of week one — turning AgentMesh into her calling card.

### 4.4 MCP Server Author — "Devon"
- Publishes an internal MCP server exposing the company's CRM to any agent that wants it.
- Pain: no way to know which agents are calling it, no rate limiting, no audit trail for compliance.
- Success looks like: he points the MCP Gateway at his server, agents connect through the Gateway URL instead of directly, and he gets an audit log and per-caller rate limits with zero changes to his server code.

## 5. Primary Workflows

1. **Instrument → Trace → Debug.** Add SDK → agent run automatically produces a trace → engineer inspects the trace DAG in the Web Console.
2. **Trace → Replay → Fix → Verify.** Select a failed trace → hit Replay → step through spans with recorded tool I/O → patch the agent code → re-run the replay against the *new* code with the *same* recorded inputs to verify the fix.
3. **Register → Gate → Audit.** Register an MCP server in the Registry → point agents at the Gateway URL instead of the raw server → review the audit log and cost dashboard per caller.
4. **Alert → Triage → Resolve.** Anomaly Detector flags a cost spike or loop → Alerting Service pings Slack → engineer opens the linked trace directly from the Slack message.

## 6. Feature Roadmap Summary

(Full feature-by-feature breakdown with complexity/dependencies/priority lives in `Feature Roadmap.md`. This section summarizes scope boundaries only.)

- **MVP (Milestones 1–4):** ingestion SDK (Python + TypeScript), Collector, Trace Store, trace DAG viewer, cost dashboard, API-key auth.
- **Post-MVP Phase 1 (Milestones 5–6):** terminal CLI, MCP Gateway + Registry, guardrail policy engine.
- **Post-MVP Phase 2 (Milestone 7):** deterministic Replay Engine, Anomaly Detector, LLM-assisted trace summarization.
- **Post-MVP Phase 3 (Milestone 8+):** hosted SaaS tier, SSO/enterprise auth, multi-region deployment, replay-based regression test suites, guardrail policy marketplace.

## 7. MVP Scope (explicit in/out)

**In scope for MVP ("v0.1"):**
- Python SDK that wraps an agent's LLM-call and tool-call boundaries and emits OTLP spans.
- Collector service that ingests OTLP over gRPC and writes to ClickHouse.
- Trace metadata (projects, API keys) in Postgres.
- Web Console: list of traces, trace DAG view (span tree with timing, tokens, cost), basic cost dashboard (spend by project/day).
- Self-hosted deployment via Docker Compose.

**Explicitly out of scope for MVP** (deferred to stated milestone):
- TypeScript SDK (Milestone 4/parity pass) — Python ships first since LangGraph/CrewAI/AutoGen are Python-first.
- Replay Engine (Milestone 7) — requires the tool-I/O recording format finalized in Milestone 4 first.
- MCP Gateway (Milestone 6) — independent subsystem, sequenced after core tracing is stable.
- Anomaly detection, LLM-assisted summaries (Milestone 7) — depends on having a meaningful trace corpus to tune against.
- Hosted multi-tenant SaaS, SSO (Milestone 8+) — self-hosted single-tenant is the only supported deployment at MVP.

## 8. Post-MVP Roadmap

See `Milestones.md` for the full milestone-by-milestone plan (M5–M8) and `Feature Roadmap.md` for the Advanced/Innovative feature tiers that populate it.

## 9. Long-Term Vision

See `Vision.md` §7. From a requirements standpoint, the long-term roadmap implies two structural commitments made starting at MVP:

1. The trace schema must be versioned from day one (Milestone 1) so that a future replay-based regression-testing product can consume Milestone-1-era traces without a migration.
2. The MCP Gateway must be built as a standalone reverse proxy (not embedded in the Collector) so it can be adopted independently by teams who want MCP governance but not full tracing — widening the addressable market for the eventual "MCP gateway marketplace" vision.
