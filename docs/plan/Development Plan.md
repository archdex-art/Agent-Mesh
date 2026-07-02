# AgentMesh — Development Plan

This document is the operational companion to `Milestones.md`: it defines *how* the work gets executed, what "done" means at each level, and how the plan documents in this folder relate to one another during actual implementation.

## 1. How This Plan Is Organized

| Document | Answers |
|---|---|
| `Vision.md` | Why does AgentMesh exist, for whom, and what's the long-term bet? |
| `Product Requirements.md` | What exactly are we building for the MVP, and what is explicitly deferred? |
| `Architecture.md` | What are the system's modules and how do they fit together? |
| `System Design.md` | How do the hardest subsystems (ingestion, replay, MCP gateway) actually work, in data-flow and schema detail? |
| `Feature Roadmap.md` | What features exist, at what complexity/priority, with what dependencies? |
| `Technical Roadmap.md` | What technology is used where, and why was each alternative rejected? |
| `Repository Structure.md` | Where does new code go, and why? |
| `Milestones.md` | In what order is this built, with what goals/risks/success criteria per phase? |
| `Risks.md` | What could go wrong at every layer, and what's the standing mitigation? |
| `Development Plan.md` (this document) | How do we actually execute, week to week, and how do we know we're on track? |

Read order for a new contributor: `Vision.md` → `Product Requirements.md` → `Architecture.md` → `Milestones.md`, consulting `System Design.md`, `Feature Roadmap.md`, `Technical Roadmap.md`, and `Repository Structure.md` as needed once implementation begins on a specific milestone.

## 2. Execution Model

AgentMesh is built by a single engineer working part-time, consistent with the freelance-portfolio origin of this project (see the prior portfolio strategy this plan grows out of). The execution model reflects that constraint directly rather than pretending a team exists:

- **One milestone in flight at a time.** Milestones are sequenced with explicit dependencies (`Milestones.md`) precisely so there is never a need to context-switch between unrelated subsystems mid-week.
- **Every milestone ends with a runnable artifact**, not a partial state — each milestone's "Success criteria" is a concrete, testable scenario (e.g., M2: "a trace appears in a query within 2 seconds"), not a checklist of merged PRs. This is the same completeness bar applied to the rest of this engineering practice: no milestone is "mostly done."
- **The golden-trace fixture corpus (from `examples/`) is built incrementally, not upfront.** Milestone 2 introduces the first example agent as an integration-test fixture; Milestone 3 adds three more; Milestone 7 reuses all four as the replay regression suite. This avoids the common trap of over-investing in test fixtures before there's a system to test.

## 3. Definition of Done (applies to every milestone)

A milestone is not complete until all of the following hold, in addition to its own stated success criteria:

1. **Tests exist and pass** for the new functionality at the layer appropriate to it (unit tests for pure logic, integration tests via `testcontainers` for anything touching ClickHouse/Postgres/Redis, golden-trace regression tests for anything touching the Replay Engine) — per `Technical Roadmap.md` §7.
2. **The relevant plan document is updated if reality diverged from the plan.** If Milestone 3 discovers that AutoGen's adapter genuinely cannot map cleanly onto the `agent.handoff` span kind, that limitation is documented in `Architecture.md` §3's mapping table, not silently absorbed into tribal knowledge.
3. **No new undocumented dependency between services.** Any new cross-service call is reflected in the `proto/` contracts and, if it changes a service boundary, in `Architecture.md` §2.
4. **The Docker Compose self-host path still works end to end** after the milestone's changes — this is checked at the end of every milestone, not deferred to Milestone 8, because a regression here silently compounds the longer it goes unnoticed (per `Risks.md`, Product Risks — "adoption friction").

## 4. What "MVP Shipped" Means

Per `Product Requirements.md` §7 and `Milestones.md` (M4), "MVP shipped" is defined as a specific, demonstrable scenario, not a date:

> A person who has never seen AgentMesh before can clone the repository, run `docker compose up`, instrument the bundled example LangGraph agent with three lines of Python, run the agent, and see the resulting trace's full span DAG and cost breakdown in the Web Console — all within 15 minutes and without reading anything beyond the README.

This scenario is the acceptance test for the MVP as a whole, run manually at the close of Milestone 4, and is the basis for the demo video called for in the broader freelance-portfolio go-to-market plan (leading with the replay capability once Milestone 7 ships, per `Risks.md`'s "yet another observability tool" mitigation).

## 5. Design-Partner Feedback Loop

Because `Vision.md` §8 documents several unvalidated assumptions (A1–A5), the plan deliberately creates checkpoints to test them against real usage rather than only internal dogfooding:

- **After Milestone 4 (MVP):** recruit one or two design partners (freelance clients, or a team encountered through the freelance-strategy network) to self-host and instrument a real agent. Their friction points directly inform Milestone 5/6 prioritization — e.g., if instrumentation friction (A1) turns out to be higher than expected, the TypeScript SDK or an additional framework adapter may need to be pulled forward ahead of the MCP Gateway.
- **After Milestone 6 (MCP Gateway):** validate Assumption A3 (MCP governance is a real, unmet need) directly with an MCP-server-owning design partner, since this is the assumption with the least existing market evidence.
- **After Milestone 7 (Replay):** measure the fraction of real design-partner traces that replay cleanly in execution mode, directly testing Assumption A2's determinism-boundary claim with production data rather than only the curated `examples/` fixtures.

## 6. Change Management for This Plan

This plan is a living document, not a one-time artifact frozen at project kickoff:

- Any milestone that reveals a scope, architecture, or technology decision that contradicts an earlier document (e.g., ClickHouse proving unworkable for self-hosters, per `Risks.md`) triggers an update to the relevant document (`Technical Roadmap.md` §4 in that example) **before** the next milestone begins, so the plan never silently drifts out of sync with the actual system.
- New features discovered mid-build that don't fit the current milestone are added to `Feature Roadmap.md` at the appropriate tier (Core/Advanced/Innovative) and sequenced into a future milestone in `Milestones.md` — never bolted on ad hoc to the milestone currently in flight, to protect the "one milestone in flight at a time" execution model in §2.

## 7. Immediate Next Action

Per this plan, the next concrete step is **Milestone 1 — Foundation**: scaffold the monorepo per `Repository Structure.md`, define the versioned span schema per `System Design.md` §2.1, and stand up the `docker-compose.yml` local environment. No production code beyond schema/scaffolding is written until that foundation is in place and validated against the Milestone 1 success criteria in `Milestones.md`.
