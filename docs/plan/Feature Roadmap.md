# AgentMesh — Feature Roadmap

Every feature is scored for **Technical Complexity** (Low/Medium/High/Very High) and **Priority** (P0 = MVP blocker, P1 = high-value post-MVP, P2 = differentiating but deferrable). Dependencies reference other features or architectural components.

## Core Features (MVP)

| Feature | Description | User Value | Complexity | Dependencies | Priority |
|---|---|---|---|---|---|
| Python SDK — span capture | Wrap LLM/tool calls, emit OTLP spans, local buffering + retry | Zero-friction instrumentation for the majority of agent frameworks (Python-first) | Medium | OTel span schema (Milestone 1) | P0 |
| Collector — OTLP ingestion | Receive, validate, batch-insert spans into ClickHouse | Reliable trace delivery without customer-side infra | Medium | ClickHouse schema | P0 |
| Trace Store schema | ClickHouse spans table + Postgres control-plane schema | Durable, queryable trace history | Medium | none | P0 |
| Trace list + search | Web Console view: list traces by project, filter by status/date/cost | Fast triage of recent runs | Low | Query API | P0 |
| Trace DAG viewer | Render a trace's span tree with timing, tokens, cost per span | The core debugging surface — see the actual decision graph | Medium | Trace Store | P0 |
| Cost dashboard (basic) | Spend by project/day, top traces by cost | Immediate, visceral value ("see your API bill before the invoice") | Low | Cost Engine (basic) | P0 |
| API-key auth | Project-scoped keys for SDK ingestion + Query API | Minimum viable multi-tenant safety | Low | Postgres schema | P0 |
| Docker Compose self-host | One-command local/dev deployment of the full stack | Removes the single biggest adoption barrier (see Vision.md A4) | Medium | all of the above | P0 |

## Advanced Features (Post-MVP, significantly improve the product)

| Feature | Description | User Value | Complexity | Dependencies | Priority |
|---|---|---|---|---|---|
| TypeScript SDK | Feature parity with the Python SDK | Unlocks Node-based agent stacks (OpenAI Agents SDK's TS support, custom Node agents) | Medium | Python SDK's span schema (reuse, don't redesign) | P1 |
| Framework reference integrations (LangGraph, CrewAI, AutoGen, OpenAI Agents SDK) | Thin adapter packages translating each framework's native hooks onto the AgentMesh span model | Proves "framework-agnostic" claim concretely; removes manual wrapping for the most common stacks | High | Core SDK | P1 |
| `agentmesh` CLI — live tail | Terminal UI streaming spans in real time | Debugging without leaving the terminal; strong demo value | Medium | Realtime Gateway | P1 |
| MCP Gateway (auth + audit) | Reverse proxy adding OAuth 2.1 + audit logging in front of any MCP server | Fills the governance gap the MCP spec explicitly leaves open | High | Auth Service, Registry | P1 |
| MCP Registry | Catalog of registered MCP servers with version pinning | Discoverability + a single source of truth for "which servers exist" | Medium | Postgres schema | P1 |
| Guardrail policy engine (declarative DSL) | YAML/JSON rules: rate limits, allow/deny lists, payload shape checks, evaluated by the Gateway | Turns the Gateway from "just a proxy" into an actual security control | High | MCP Gateway | P1 |
| Deterministic Replay Engine (trajectory mode) | Read-only reconstruction/step-through of a historical trace | Debugging without re-execution risk; the safest, simplest replay mode to ship first | High | Trace Store, blob storage | P1 |
| Deterministic Replay Engine (execution mode) | Re-run current agent code against recorded tool I/O via the SDK's replay shim | The signature differentiator — verify a fix against real historical failure data | Very High | Trajectory-mode replay, SDK replay shim | P1 |
| Anomaly Detector (rule-based) | Loop detection, cost-spike detection, guardrail-violation aggregation | Proactive alerting instead of after-the-fact discovery | Medium | Redis span stream, Alerting Service | P1 |
| Alerting Service (Slack/PagerDuty/webhook) | Deliver anomaly/cost alerts to existing on-call tooling | Meets teams where they already work | Low | Anomaly Detector | P1 |
| Per-caller cost attribution through the Gateway | Cost Engine extended to attribute MCP tool cost to the calling agent identity | Answers "which agent is running up our tool bill," not just "which LLM call" | Medium | MCP Gateway, Cost Engine | P2 |

## Innovative Features (Differentiators — post-MVP, longer horizon)

| Feature | Description | User Value | Complexity | Dependencies | Priority |
|---|---|---|---|---|---|
| LLM-assisted trace summarization | "Why did this trace fail?" plain-English summary generated from the span tree | Faster triage for engineers unfamiliar with a given trace | Medium | Trajectory replay, an LLM call budget of AgentMesh's own | P2 |
| Replay-based regression test suites | Pin a set of historical traces as a regression corpus; re-run them against a new agent-code version or model and diff outcomes automatically in CI | Turns "did my change break anything" into a runnable test, the single most startup-shaped feature on this list | Very High | Execution-mode replay, CI integration | P2 |
| WASM-sandboxed custom guardrail policies | Beyond the declarative DSL: arbitrary user code (compiled to WASM) evaluated per tool call with strict sandboxing | Supports guardrail logic too complex for a declarative rule (e.g., calling an internal risk-scoring service) | Very High | Guardrail policy engine, WASM runtime (Wasmtime) | P2 |
| Cross-framework migration diffing | Given two traces of "the same task" run on two different frameworks (e.g., a CrewAI version and a LangGraph rewrite), diff their tool-call trajectories side by side | Directly de-risks framework migrations — a real, painful, undifferentiated task today | High | Trajectory replay, trace-similarity matching | P2 |
| Guardrail policy marketplace | Curated, shareable guardrail policy packs (e.g., "PII redaction," "SQL-injection-safe tool calls") installable from the Registry | Community/ecosystem flywheel; the clearest path to the "startup potential" vision | High (mostly product/ecosystem work, not core engineering) | Guardrail policy engine, public Registry | P2 |
| Session-level cost/behavior analytics | Roll cost and anomaly signals up from individual traces to a full user session (multi-turn conversation) | Answers product questions ("which user flows are most expensive/most failure-prone"), not just engineering ones | Medium | Sessions data model, Cost Engine | P2 |

## Prioritization Rationale

- **P0 features are the minimum set that makes AgentMesh usable for Use Case 1 (post-incident debugging) and Use Case 2 (pre-launch cost audit)** from `Vision.md` §4 — nothing else in the MVP is justified if those two workflows don't work end-to-end.
- **Execution-mode replay is Very High complexity and P1, not P0**, because trajectory-mode replay alone already delivers most of the debugging value with a fraction of the engineering risk (no replay shim, no interception correctness concerns). Shipping trajectory mode first also produces the real trace corpus needed to validate Assumption A2 in `Vision.md` before committing to execution mode.
- **MCP Gateway is sequenced after core tracing (P1, Milestone 6)**, not P0, because it is architecturally independent — building it first would delay the tracing MVP without validating the tracing value proposition, which is the larger of the two bets.
