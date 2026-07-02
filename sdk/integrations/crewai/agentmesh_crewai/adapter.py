"""AgentMesh adapter for CrewAI.

Maps CrewAI's two crew/agent-level callback hooks onto AgentMesh's three
adapter-usable span kinds (Architecture.md §3):

  - `Agent.step_callback` (invoked once per ReAct step CrewAI's own agent
    executor completes -- i.e. once the LLM has produced either an
    `AgentAction`, a tool-use decision whose tool has *already been
    executed* by the time the callback fires, or an `AgentFinish`, the
    task's final answer) -> `llm.call`.
  - `Crew.task_callback` (invoked once a `Task` fully completes) ->
    `agent.handoff`, matching this integration's original design
    decision to treat a task finishing (and its result becoming
    available to whatever task/agent runs next) as a delegation event.

Unlike LangGraph/AutoGen/the OpenAI Agents SDK, CrewAI does not give an
adapter a *separate* start callback for either of these events: both
`step_callback` and `task_callback` fire once, after the fact, with no
call-stack relationship to any "start" event a caller could hang a
context manager off of. The pre-`agentmesh_integrations_common` version
of this adapter worked around that by opening and immediately closing a
`tracer.start_span(...)` context manager inside each callback body. That
happened to look correct for the one demo script this integration
shipped with, because the script wraps the entire `crew.kickoff()` call
in its own outer span -- keeping something on `Tracer`'s internal
call-stack for every step/task span to attach to as a parent -- but it
was not a *correct* general solution:

  - Call `instrument_crew(crew, tracer)` and then `crew.kickoff()`
    *without* wrapping it in an outer span (the natural thing to do,
    for any caller who doesn't know about this quirk) and every single
    step/task span becomes the root of its own one-span trace:
    `Tracer._span_stack` is empty every time a callback fires, so
    `parent_span_id` is always `None` and a fresh `trace_id` gets minted
    per span.
  - Even when the caller *does* wrap `kickoff()` in an outer span, every
    step and every task's handoff span ends up a flat sibling of every
    other one under that single root -- nothing nests a task's own
    steps underneath that task's handoff span, because the `with` block
    for the handoff span opens and closes synchronously inside
    `task_callback`, which fires strictly *after* all of that task's
    steps (and their own already-closed `with` blocks) happened.

`CrewAIAdapter` fixes both problems by building its own explicit parent
chain through `agentmesh_integrations_common.context.SpanTracker`,
independent of anything on `Tracer`'s call stack:

  - `instrument()` opens one structural (`kind=None`) root node the
    moment it runs -- not lazily, on the first callback -- so every span
    this adapter ever emits for `crew` shares one `trace_id` regardless
    of whether the caller wraps `kickoff()` in anything at all.
  - `instrument()` also opens every task's `agent.handoff` span up
    front, parented to that root and keyed by the task's own `Task.id`.
    This is the one deliberate accuracy trade-off, documented in
    `LIMITATIONS.md`: CrewAI exposes no "task started" hook, so a task's
    handoff span technically opens at `instrument()` time rather than
    the moment that task actually starts running (span *duration* is
    therefore not meaningful) -- but it is what lets every step that
    happens *during* a task's run resolve that task's span as its
    parent, which the original open-and-close-per-callback design could
    never do, since a task's handoff span didn't exist yet while its
    steps were happening.
  - `step_callback` resolves "which task is this step part of" via a
    single cursor over `crew.tasks`, advanced every time `task_callback`
    fires. This assumes CrewAI's default `Process.sequential` execution
    (tasks run to completion one at a time, in `crew.tasks` order) --
    see `LIMITATIONS.md` for what this does not cover.

Known limitation (documented here and in `LIMITATIONS.md` rather than
silently swallowed): `SpanTracker.start()` does not expose a way to set
`Span.input` before `finish()` is called, so this adapter -- like every
other Milestone 3 adapter built on `agentmesh_integrations_common` --
never populates `Span.input` for the spans it emits, only `attributes`
and `output`.

A thin `instrument_crew(crew, tracer)` function is kept for backward
compatibility with existing callers (`examples/crewai-research-crew` and
`tests/test_crewai.py`); it just constructs a `CrewAIAdapter` and calls
`.instrument(crew)`.
"""

from __future__ import annotations

import json
import logging
import uuid
from typing import Any

import agentmesh
from agentmesh_integrations_common.adapter import FrameworkAdapter
from agentmesh_integrations_common.context import UnknownSpanError
from agentmesh_integrations_common.spans import FRAMEWORK_ATTR, SpanKind, SpanStatus

logger = logging.getLogger("agentmesh_crewai")

__all__ = ["CrewAIAdapter", "instrument_crew"]


def _step_output_text(step_output: Any) -> str:
    """Best-effort extraction of a `step_callback` payload's text.

    Mirrors this adapter's pre-`SpanTracker` behavior exactly: CrewAI's
    `AgentFinish`/`AgentAction` dataclasses have no `return_values`
    attribute in currently-supported CrewAI versions, but the check is
    kept for compatibility with the older CrewAI releases the original
    implementation targeted (and is harmless to keep -- it just never
    matches against today's CrewAI).
    """
    if hasattr(step_output, "return_values"):
        return json.dumps(step_output.return_values, default=str)
    if hasattr(step_output, "text"):
        return step_output.text
    return str(step_output)


class CrewAIAdapter(FrameworkAdapter):
    """Instruments a CrewAI `Crew` instance to emit AgentMesh spans.

    See the module docstring for the full rationale. Call
    `instrument(crew)` once per crew, before `crew.kickoff()`.

    Sets `self.trace_id` (the shared `trace_id` every span this adapter
    emits for `crew` will carry) once `instrument()` returns, so a caller
    that wants to log/correlate it (the way the original implementation's
    example script printed an outer `with tracer.start_span(...)` span's
    `trace_id`) doesn't have to reach into `self.spans`' internals to get
    it.
    """

    trace_id: str | None = None

    def instrument(self, target: Any) -> None:
        crew = target

        # Structural root: never exported (kind=None), exists purely so
        # every span this adapter emits for `crew` shares one trace_id
        # and resolves to an explicit parent instead of whatever happens
        # to be on `Tracer`'s ambient call-stack. See module docstring.
        crew_ext_id = f"crewai-crew:{uuid.uuid4()}"
        self.spans.start(crew_ext_id, None, "crewai-crew")
        self.trace_id = self.spans.active_trace_id(crew_ext_id)

        tasks = list(getattr(crew, "tasks", []))
        for task in tasks:
            agent_name = getattr(getattr(task, "agent", None), "role", "unknown_agent")
            self.spans.start(
                str(task.id),
                SpanKind.AGENT_HANDOFF,
                f"handoff_from_{agent_name}",
                parent_external_id=crew_ext_id,
                attributes={
                    FRAMEWORK_ATTR: "crewai",
                    "crewai.task_description": task.description,
                },
            )

        # Single cursor over `crew.tasks`, in execution order, shared by
        # every agent's step_callback and by task_callback: this is what
        # resolves "which task is currently in flight" explicitly,
        # instead of relying on `Tracer`'s per-call-stack ambient state
        # (which, per the module docstring, never reflects a task
        # boundary at all in the original implementation). Correct for
        # `Process.sequential`; see `LIMITATIONS.md`.
        cursor = {"index": 0}

        def _current_task_ext_id() -> str | None:
            idx = cursor["index"]
            if 0 <= idx < len(tasks):
                return str(tasks[idx].id)
            return None

        original_task_callback = crew.task_callback

        def wrapped_task_callback(task_output, *args, **kwargs):
            task_ext_id = _current_task_ext_id()
            if task_ext_id is None:
                logger.warning(
                    "crewai task_callback fired beyond the tracked task list "
                    "(index %d of %d); skipping span for this delegation",
                    cursor["index"],
                    len(tasks),
                )
            else:
                try:
                    out_str = getattr(task_output, "raw_output", str(task_output))
                    self.spans.finish(task_ext_id, status=SpanStatus.OK, output=out_str)
                except UnknownSpanError:
                    # A mismatched start/finish pair is a real bug -- surface it.
                    raise
                except Exception as e:
                    logger.warning(f"failed to trace crewai task: {e}")
                finally:
                    cursor["index"] += 1

            if original_task_callback:
                original_task_callback(task_output, *args, **kwargs)

        crew.task_callback = wrapped_task_callback

        def wrapped_step_callback(step_output, *args, **kwargs):
            step_ext_id = str(uuid.uuid4())
            parent_ext_id = _current_task_ext_id() or crew_ext_id
            try:
                self.spans.start(
                    step_ext_id,
                    SpanKind.LLM_CALL,
                    "crewai-step",
                    parent_external_id=parent_ext_id,
                    attributes={FRAMEWORK_ATTR: "crewai"},
                )
                self.spans.finish(
                    step_ext_id,
                    status=SpanStatus.OK,
                    output=_step_output_text(step_output),
                )
            except UnknownSpanError:
                raise
            except Exception as e:
                logger.warning(f"failed to trace crewai step: {e}")

        for agent in getattr(crew, "agents", []):
            original_agent_step_callback = getattr(agent, "step_callback", None)

            def make_agent_callback(orig):
                def agent_step_callback(step_output, *args, **kwargs):
                    wrapped_step_callback(step_output, *args, **kwargs)
                    if orig:
                        orig(step_output, *args, **kwargs)

                return agent_step_callback

            agent.step_callback = make_agent_callback(original_agent_step_callback)


def instrument_crew(crew: Any, tracer: agentmesh.Tracer) -> None:
    """Backward-compatible functional entry point.

    Constructs a `CrewAIAdapter(tracer)` and calls `.instrument(crew)`;
    kept so existing callers (`examples/crewai-research-crew/main.py`,
    `tests/test_crewai.py`) do not need to change. See `CrewAIAdapter`
    for the actual instrumentation logic.
    """
    CrewAIAdapter(tracer).instrument(crew)
