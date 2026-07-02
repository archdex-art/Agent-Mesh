# AgentMesh — Risks and Mitigations

## 1. Technical Risks

| Risk | Impact | Likelihood | Mitigation |
|---|---|---|---|
| **Replay determinism boundary is narrower than assumed** — some agent non-determinism turns out to come from sources the SDK can't record (e.g., wall-clock reads, unwrapped global state, non-instrumented I/O). | High — undermines the core differentiator | Medium | Ship trajectory-mode replay first (Milestone 7 checkpoint) to validate DAG reconstruction independently of execution replay; document the determinism boundary explicitly in SDK docs and the Replay UI itself (never silently claim perfect replay); treat "what fraction of real traces replay cleanly" as a tracked product metric from Milestone 7 onward, not an assumption. |
| **Framework adapter maintenance burden** — LangGraph/CrewAI/AutoGen/OpenAI Agents SDK all ship breaking changes on their own release cadence; four adapters is four points of ongoing maintenance. | Medium — adapters silently drift out of date | High (this is a certainty over a multi-year horizon, not a possibility) | Adapters are versioned and released independently from core SDK (`Repository Structure.md`); each adapter has its own CI job pinned to a matrix of supported framework versions that fails loudly (not silently) when a framework upstream breaks compatibility. |
| **OAuth 2.1 implementation bugs in the MCP Gateway** — the single highest-stakes piece of code in the system; an auth bug could wrongly allow unauthorized tool calls. | Very High — a security incident directly undermines the "governance" pillar of the value proposition | Low (if mitigated correctly) | Use an audited OAuth library, never hand-roll token validation; fail-closed by design (`Architecture.md` §17); dedicated security review at Milestone 6 and again at Milestone 8. |
| **ClickHouse operational complexity for self-hosters** — ClickHouse has a steeper ops learning curve than Postgres (partitioning, merge behavior, TTL tuning) for a team self-hosting without a dedicated data engineer. | Medium — adoption friction for the primary (self-hosted) deployment path | Medium | Docker Compose profile ships with sane, pre-tuned defaults so most self-hosters never touch ClickHouse configuration directly; TimescaleDB is documented as an evaluated fallback if this friction proves too high in practice (`Technical Roadmap.md` §4). |

## 2. Product Risks

| Risk | Impact | Likelihood | Mitigation |
|---|---|---|---|
| **Instrumentation friction is higher than Assumption A1 predicts** — teams balk at adding SDK wrapping even at "just a decorator" level. | High — kills the funnel at step one | Medium | Framework reference integrations (Milestone 3) specifically target zero-manual-wrapping for the four most common stacks; the manual decorator API remains available as a fallback for custom loops, not the primary path once integrations exist. |
| **Web Console scope creep** — temptation to build replay/anomaly UI before those backends exist, or to chase UI polish before the core debugging loop works. | Medium — delays MVP ship date | Medium | Milestone 4's success criteria are explicit and UI scope is fixed to exactly the P0 features in `Feature Roadmap.md`; anything else is deferred by construction, not by discipline alone. |
| **"Yet another observability tool" skepticism** — target users (Priya, Marcus, Elena in `Product Requirements.md`) already have Langfuse/LangSmith/Helicone or nothing, and may not see the differentiation. | High — no adoption regardless of engineering quality | Medium | The two features that don't exist elsewhere (execution-mode replay, MCP governance) are prioritized precisely because they are not "me too" — go-to-market messaging and the demo video (per the freelance-portfolio strategy) must lead with replay, not with "yet another trace viewer." |

## 3. Performance Risks

| Risk | Impact | Likelihood | Mitigation |
|---|---|---|---|
| **SDK overhead slows down the customer's agent** — span capture, especially recording full tool I/O for replay, adds latency to the hot path the agent runs on. | Medium — a slow SDK is worse than no SDK for a latency-sensitive product like a support agent | Medium | Async, batched export off the request-handling thread (`Architecture.md` §17); large payloads write to blob storage asynchronously, never blocking the agent's tool-call return path; SDK overhead is a tracked, benchmarked metric (not just assumed acceptable) starting in Milestone 2. |
| **ClickHouse batch-insert tuning is wrong, causing either high ingestion latency or excessive small-batch write amplification.** | Medium | Medium | Explicit load-testing gate before Milestone 2 is declared done (`Milestones.md`, M2 risk note), not deferred to a later "performance pass." |

## 4. Scalability Risks

| Risk | Impact | Likelihood | Mitigation |
|---|---|---|---|
| **Single-node ClickHouse hits a ceiling** once a design partner's trace volume grows beyond early-stage assumptions (Vision.md A5). | Medium — degraded query performance, not data loss | Low at MVP, rising over time | Schema is `project_id`-sharding-ready from day one (`Technical Roadmap.md` §11); horizontal ClickHouse sharding by `project_id` is the documented next step, deliberately not built until real volume data justifies the added operational complexity. |
| **Realtime Gateway fan-out doesn't scale past a handful of concurrent live-tailing sessions per project.** | Low — degrades a nice-to-have (live tail) feature, not core tracing | Low | Redis-channel sharding by `project_id` is the documented fix, deferred until usage data shows it's needed (`System Design.md` §5). |

## 5. Security Risks

| Risk | Impact | Likelihood | Mitigation |
|---|---|---|---|
| **Sensitive data (PII, secrets) captured in recorded tool I/O** — since AgentMesh records full inputs/outputs for replay, it becomes a high-value target and a compliance liability if a customer's agent handles sensitive data. | Very High | High (this will happen unless designed against) | Redaction hooks in the SDK (configurable field/regex-based scrubbing before export, applied at the SDK layer so sensitive data never leaves the customer's process in the first place) — this must ship no later than Milestone 4 alongside the retention system, not deferred as a "nice to have." Documented explicitly in onboarding as a required configuration step for any production use. |
| **MCP Gateway as a new attack surface** — introducing a proxy in front of previously-direct tool access creates a new component that could be compromised or misconfigured. | High | Medium | Fail-closed design, audited OAuth library, dedicated security review at Milestone 6 and 8 (cross-referenced with Technical Risks above). |
| **Blob storage misconfiguration exposing tool I/O payloads** (e.g., a misconfigured public S3 bucket). | High | Low with correct defaults | Self-hosted Docker Compose profile defaults MinIO to private, authenticated access only; Helm chart's default values enforce the same; documented as a pre-flight check in the self-hosting guide (Milestone 8 deliverable). |

## 6. Maintenance Risks

| Risk | Impact | Likelihood | Mitigation |
|---|---|---|---|
| **Solo/small-team bus factor** — the entire system (8 services, 2 SDKs, 4 framework adapters, a CLI, and a Console) is a lot of surface area for one person to maintain long-term. | Medium — maintenance burden could stall feature velocity | High over a multi-year horizon | Deliberately boring, well-documented technology choices (`Technical Roadmap.md`) minimize the "only one person understands this" risk; the monorepo-with-clear-boundaries structure (`Repository Structure.md`) means any future contributor can own a single `services/` folder without understanding the whole system first. |
| **Schema drift between the versioned span format and older SDK/Collector versions during rolling upgrades.** | Medium — silently malformed data | Medium | `schema_version` field on every span (`Technical Roadmap.md` §9) makes mismatches detectable rather than silent; the Collector rejects (not silently accepts) spans with an unrecognized schema version, surfacing the problem immediately instead of corrupting the trace store. |
