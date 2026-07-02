# AgentMesh — Vision

## 1. What AgentMesh Is

**AgentMesh is a framework-agnostic control plane for AI agents** — a self-hostable system that gives teams building agentic products the same operational maturity that Datadog gave to microservices and that Temporal gave to distributed workflows, but purpose-built for the failure modes of LLM-driven, tool-using, multi-step agents.

Concretely, AgentMesh provides four capabilities no single existing tool combines:

1. **Framework-agnostic execution tracing** — capture the full DAG of an agent run (LLM calls, tool calls, sub-agent handoffs, retries, token/cost) regardless of whether the agent is built on LangGraph, CrewAI, AutoGen, the OpenAI Agents SDK, or a hand-rolled loop.
2. **Deterministic replay** — re-run any historical agent trace exactly, with recorded or mocked tool responses, so a developer can step through *why* a specific production run failed without needing to reproduce the bug live.
3. **MCP-native governance** — a gateway that sits in front of any Model Context Protocol (MCP) server and adds the things the open MCP spec deliberately leaves to implementers: authentication (OAuth 2.1), audit trails, per-tool guardrail policies, and cost accounting.
4. **Cost and anomaly intelligence** — per-agent, per-tool, per-user spend tracking, with automatic detection of runaway loops, cost spikes, and guardrail violations.

AgentMesh is **not** another agent-building framework and **not** another coding assistant. It is infrastructure that any of those tools plug into.

## 2. Problem Statement

Teams shipping agentic AI products in 2026 have solved "how do I build an agent" (LangGraph, CrewAI, AutoGen, and the OpenAI Agents SDK are all mature, well-documented answers). They have not solved "how do I run this agent in production with confidence":

- **No visibility into non-deterministic failures.** An agent looped nine times before giving up, or picked the wrong tool, or hallucinated a parameter — and there is no artifact that lets an engineer see *exactly* what happened, in what order, with what inputs.
- **No reproducibility.** Because tool responses (search results, API replies, file contents) change between the failing run and any attempt to reproduce it, "just run it again" doesn't work. Bugs that took five tool calls to manifest are effectively undebuggable without a recording.
- **No cost containment.** Multi-agent systems can multiply an unbounded conversational loop into hundreds of dollars of API spend before anyone notices. Existing LLM proxies (Helicone) show aggregate cost but not *which agent trajectory* caused it.
- **MCP adoption is outpacing MCP governance.** MCP is now the de facto integration standard (10,000+ public servers, 1,000+ organizations in production as of mid-2026), but the specification intentionally does not mandate authentication, audit logging, or rate limiting — every team is left to bolt this on themselves, or skip it.
- **Existing observability tools force a framework choice.** LangSmith is excellent but LangChain/LangGraph-native; Langfuse is framework-agnostic and open-source but has no first-class deterministic replay; Arize Phoenix is OTel-native but ML-monitoring-flavored UX; Helicone is a proxy that sees requests, not multi-step agent trajectories or MCP tool traffic.

## 3. Target Users

| Persona | Description | Primary Need |
|---|---|---|
| **Agentic Product Engineer** | Builds customer-facing AI agents (support copilots, research assistants, coding agents) at a Series A–C startup. Ships weekly, owns reliability. | Fast root-cause of a specific failed run; cost guardrails before a launch. |
| **Platform/DevEx Lead** | Owns internal agent infrastructure at a larger org running multiple agent products across teams built on different frameworks. | A single pane of glass across LangGraph/CrewAI/AutoGen/custom agents; standardized MCP governance. |
| **AI Consultant / Freelancer** | Hired to audit, harden, or debug a client's existing agent system. | A tool they can drop into an unfamiliar codebase in an afternoon and immediately get trace visibility — no framework migration required. |
| **MCP Server Author** | Publishes/maintains MCP servers (internal or public) and needs to know who is calling them, how often, and whether calls look abusive. | Auth, rate limiting, and audit logging without hand-rolling a gateway. |

## 4. Primary Use Cases

1. **Post-incident debugging.** "Agent run #48213 charged a customer twice — replay it, step through the tool calls, find the race condition."
2. **Pre-launch cost audit.** "Before we ship this agent to 10,000 users, show me the cost distribution across 500 historical test runs and flag any trajectory that could loop indefinitely."
3. **Cross-framework migration.** "We're moving from CrewAI to LangGraph — AgentMesh traces both identically, so our dashboards and alerts don't need to be rebuilt."
4. **MCP security hardening.** "We exposed an internal MCP server to a partner team — route it through the AgentMesh Gateway so every call is authenticated, rate-limited, and logged."
5. **Regression detection.** "We swapped GPT-4.1 for a new model version — replay last week's 200 traces against the new model and diff the tool-call trajectories."

## 5. Core Value Proposition

> **See every decision your agents make, replay any failure exactly, and govern every tool they call — without rewriting your agent stack.**

AgentMesh wins on being the connective tissue rather than another silo: it speaks OpenTelemetry (so it ingests from anything already OTel-instrumented), speaks MCP (so it governs any MCP server without modification), and stores everything in an open, queryable schema (so teams are never locked into a proprietary trace format).

## 6. What Makes AgentMesh Different

| Dimension | Incumbent gap | AgentMesh's answer |
|---|---|---|
| Framework lock-in | LangSmith is deepest for LangChain/LangGraph only | OTel/OpenInference-based ingestion works identically for any framework |
| Reproducibility | LangSmith/Langfuse/Arize show traces but do not deterministically **re-execute** a past run | Replay Engine re-runs a trace with recorded tool I/O, byte-for-byte reproducible |
| MCP governance | The MCP spec explicitly defers auth/audit/rate-limiting to implementers; no dominant gateway exists yet | MCP Gateway + Registry ship auth, audit, guardrails, and cost accounting as first-class, protocol-native features |
| Cost attribution | Helicone tracks proxy-level spend; frameworks track it per-run at best | Cost Engine attributes spend to individual tool calls and sub-agents inside a single trace |
| Deployment model | LangSmith is closed SaaS; Langfuse is open-source and self-hostable (the closest analog) | Open-core, self-hostable by default (Apache-2.0 core), following the model research shows already works in this space (Langfuse), with a hosted tier for teams that don't want to run ClickHouse themselves |

## 7. Long-Term Vision

AgentMesh's long-term bet is that **MCP becomes the transport layer for agent-to-tool communication the way HTTP became the transport layer for web services** — and that whoever owns the observability and governance layer for that transport becomes as embedded in the agentic AI stack as Datadog is in cloud infrastructure. The five-year vision:

1. **Year 1:** Best-in-class open-source tracing + replay for agent frameworks; the tool consultants and startups reach for first when an agent misbehaves.
2. **Year 2:** The default MCP gateway — teams register their MCP servers with AgentMesh the way they register APIs with an API gateway today.
3. **Year 3+:** A marketplace of guardrail policies, anomaly-detection models, and replay-based regression test suites, turning agent reliability engineering into a purchasable capability rather than a bespoke build for every team.

## 8. Documented Assumptions

Because this plan is produced before any user interviews or usage data exist, the following assumptions are explicit and should be validated during the MVP phase (see `Milestones.md`, Milestone 3):

- **A1 — Instrumentation friction is acceptable if it's low.** We assume teams will add an SDK import + a decorator/wrapper around their agent loop, but will *not* accept rewriting their orchestration logic. Validate by measuring time-to-first-trace for a pilot user integrating LangGraph vs. AutoGen.
- **A2 — Deterministic replay is technically feasible for the common case.** We assume most agent non-determinism is confined to LLM sampling and tool I/O (both of which can be recorded/mocked), not to genuinely non-reproducible external state (e.g., a live stock price). Validate during Milestone 7 with real traces from the reference integrations built in Milestone 3.
- **A3 — MCP governance is a wedge, not a niche.** We assume the security/audit gap identified in MCP's 2026 roadmap discussion (OAuth 2.1, audit trails, RCE prevention) is a real, currently-unmet need rather than something the MCP spec itself will solve first. Revisit if the AAIF governance body ships an official gateway/reference implementation.
- **A4 — Self-hosting is the primary adoption path.** We assume early adopters (startups, consultants) will run AgentMesh via Docker Compose/Helm rather than pay for hosted SaaS on day one, mirroring Langfuse's adoption curve. The hosted tier is a Milestone 8+ concern, not an MVP requirement.
- **A5 — ClickHouse is the right trace store at MVP scale.** We assume trace/span volume for an early-stage product (single-digit customers, thousands of traces/day) fits comfortably in a single-node ClickHouse instance. Revisit sharding/scaling only when a design partner's volume demands it (see `Risks.md`, Scalability Risks).
