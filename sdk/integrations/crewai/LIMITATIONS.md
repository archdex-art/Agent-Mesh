# CrewAI adapter limitations

Documented per the Milestone 3 test-strategy time-boxing rule: fidelity
gaps below are known and accepted, not silently dropped.

## No `Span.input` population

`agentmesh_integrations_common.context.SpanTracker.start()` does not
expose a way to set `Span.input` before `finish()` is called (it only
accepts `attributes`, threaded onto the exported span's `attributes`
dict). This adapter -- like every other Milestone 3 adapter built on
`agentmesh_integrations_common` -- never populates `Span.input`. Where
useful, start-time-only context (e.g. a task's description) is instead
recorded as a string attribute (see `CrewAIAdapter.instrument`'s
`crewai.task_description` attribute).

## `step_callback` is not invoked for a task's terminal "Final Answer" step

Confirmed against the installed `crewai` version's default agent
executor (`crewai.experimental.agent_executor.AgentExecutor`, which
`Agent.executor_class` defaults to): `step_callback` fires for every
intermediate ReAct tool-decision step (an `AgentAction`, twice per
step -- once via `crewai.utilities.agent_utils`'s tool-result handling,
once via the executor's own `_invoke_step_callback` call after the tool
runs), but it is **not** invoked at all for the step that produces the
task's final `AgentFinish` answer when that answer is reached through
the text/ReAct parsing path (`call_llm_and_parse` ->
`route_by_answer_type` -> `_route_finish_with_todos("agent_finished")`
never calls `_invoke_step_callback`). The "native tool calling" code
path (`call_llm_native_tools`) does call `_invoke_step_callback` for its
own final-answer case, but text-based fake/scripted LLMs (the only kind
used in this integration's tests and example, per Milestone 3's
no-network-calls rule) always take the ReAct/text path.

Practical effect: a task whose agent immediately returns a final answer
without ever using a tool (no `AgentAction` step) produces **zero**
`llm.call` spans for that task -- only its `agent.handoff` span (from
`task_callback`). This is a CrewAI behavior, not an
`agentmesh_crewai`/`SpanTracker` bug: no `agentmesh` API can observe an
event CrewAI itself never calls back for. `tests/test_shared_workflow.py`
accounts for this directly (its reviewer agent makes one tool call
before finishing, specifically so its `llm.call` span count is
non-zero and testable) and documents the exact call counts involved.

## Task-to-step parent resolution assumes `Process.sequential`

`CrewAIAdapter` resolves "which task is this step part of" via a single
cursor over `crew.tasks`, advanced by one every time `task_callback`
fires (see `agentmesh_crewai.adapter`'s module docstring for why: CrewAI
gives no native "task started" callback to hook a `SpanTracker.start()`
call to). This is correct for `Process.sequential` -- the process kind
this integration's tests and example use, and CrewAI's default -- where
exactly one task is ever "in flight" at a time, in `crew.tasks` order.

It is not guaranteed correct for:

- `Process.hierarchical`, where a manager agent can interleave work
  across delegate agents in an order that does not match `crew.tasks`'
  static list order.
- Tasks run with `async_execution=True`, where two tasks (and therefore
  two agents' `step_callback`s) can genuinely be in flight concurrently;
  the single shared cursor has no way to attribute a step to the correct
  one of two simultaneously-open tasks in that case.

Both are out of scope for this reference integration's tested/supported
mode (`Process.sequential`, synchronous tasks) per the Milestone 3
time-boxing rule; a step misattributed under either mode still links
somewhere in the same single trace (no orphan spans, no crash) rather
than being dropped, since the cursor always resolves to *some* tracked
task (or the crew-level structural root as a fallback).

## Task-level `agent.handoff` spans do not have an accurate start time

Every task's `agent.handoff` span is opened (via `SpanTracker.start()`)
inside `CrewAIAdapter.instrument()`, before `crew.kickoff()` ever runs --
not when that specific task actually starts executing -- because CrewAI
exposes no "task started" hook the adapter could use to open it later.
This is what lets every step that happens *during* a task's run resolve
that task's span as its parent (see the module docstring for why the
original, pre-`SpanTracker` implementation could not do this at all).
The consequence is that a handoff span's `start_time_ns` reflects
instrumentation time, not the task's actual start time, so its recorded
*duration* is not meaningful for any task after the first. Span
structure (parent/child linkage, kind, count) is unaffected.
