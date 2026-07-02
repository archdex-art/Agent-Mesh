"""Regression test: `CrewAIAdapter` against the Milestone 3 canonical
shared workflow (`examples/shared`) -- search -> read_page -> summarize
-> review -- built as a real CrewAI crew (a "Researcher" agent with the
shared `search_tool`/`read_page_tool` functions wired in as CrewAI tools,
and a "Reviewer" agent), driven end to end via `crew.kickoff()` with a
scripted fake LLM (no network, no real model).

This intentionally does NOT wrap `crew.kickoff()` in an outer
`tracer.start_span(...)` the way `tests/test_crewai.py` and
`examples/crewai-research-crew/main.py` do -- the whole point of
retrofitting this adapter onto `agentmesh_integrations_common.context.
SpanTracker` (see `agentmesh_crewai.adapter`'s module docstring) is that
correct parent/child linkage no longer depends on the caller keeping
something open on `Tracer`'s internal call-stack. Under the pre-retrofit
implementation, running `crew.kickoff()` bare like this would have
produced one disconnected single-span trace *per callback firing*
(`Tracer._span_stack` is empty every time a callback fires, so every
span mints its own fresh `trace_id`) -- `assert_single_trace` below
would fail against the old implementation and passes against the new
one.
"""

from __future__ import annotations

import pathlib
import sys
import warnings

import pytest
from pydantic import PrivateAttr

warnings.filterwarnings("ignore", category=DeprecationWarning)

from crewai import Agent, Crew, Process, Task
from crewai.llm import LLM
from crewai.tools import tool as crewai_tool

import agentmesh
from agentmesh_crewai import CrewAIAdapter
from agentmesh_integrations_common.spans import SpanKind
from agentmesh_integrations_common.testing import (
    FakeExporter,
    assert_no_orphan_spans,
    assert_single_trace,
)

_REPO_ROOT = pathlib.Path(__file__).resolve().parents[4]
sys.path.insert(0, str(_REPO_ROOT / "examples" / "shared"))

import fixtures  # noqa: E402  (examples/shared, path-inserted above)
import prompts  # noqa: E402
import tools as shared_tools  # noqa: E402


@crewai_tool("search_tool")
def _search_tool(query: str) -> str:
    """Search the AgentMesh fixture knowledge base for a topic."""
    return shared_tools.search_tool(query)


@crewai_tool("read_page_tool")
def _read_page_tool(url_or_ref: str) -> str:
    """Read the full canned content of a previously-found page or ref."""
    return shared_tools.read_page_tool(url_or_ref)


class ScriptedLLM(LLM):
    """A `CannedLLM`/`FakeLLM`-style fake that returns each of `responses`
    in order across successive `.call()` invocations (then repeats the
    last one), so a single agent can be scripted through a multi-step
    ReAct loop -- tool decision(s) followed by a final answer -- without
    a real model.
    """

    _responses: list[str] = PrivateAttr(default_factory=list)
    _index: int = PrivateAttr(default=0)

    def __init__(self, *, model: str, responses: list[str], **kwargs):
        super().__init__(model=model, **kwargs)
        self._responses = responses
        self._index = 0

    def call(self, messages, *args, **kwargs):
        i = min(self._index, len(self._responses) - 1)
        self._index += 1
        return self._responses[i]


@pytest.fixture
def tracer():
    return agentmesh.Tracer(project_id="test-proj", exporter=FakeExporter())


def _build_crew() -> Crew:
    researcher_llm = ScriptedLLM(
        model="fake/researcher",
        responses=[
            "Thought: I should search for information about the topic.\n"
            "Action: search_tool\n"
            f'Action Input: {{"query": "{prompts.RESEARCH_TOPIC}"}}',
            "Thought: Now let me read the top result in full.\n"
            "Action: read_page_tool\n"
            'Action Input: {"url_or_ref": "mcp-governance-gaps"}',
            f"Thought: I have enough information now.\nFinal Answer: {prompts.FAKE_SUMMARY}",
        ],
    )
    reviewer_llm = ScriptedLLM(
        model="fake/reviewer",
        responses=[
            "Thought: Let me double check the source material before approving.\n"
            "Action: read_page_tool\n"
            'Action Input: {"url_or_ref": "mcp-governance-gaps"}',
            f"Thought: Confirmed against the source.\nFinal Answer: {prompts.FAKE_REVIEW}",
        ],
    )

    researcher = Agent(
        role="Researcher",
        goal="Research the assigned topic using the search and read_page tools.",
        backstory="You are a meticulous research agent.",
        llm=researcher_llm,
        tools=[_search_tool, _read_page_tool],
    )
    reviewer = Agent(
        role="Reviewer",
        goal="Review a research summary for accuracy and completeness.",
        backstory="You are a careful reviewer agent.",
        llm=reviewer_llm,
        tools=[_read_page_tool],
    )

    research_task = Task(
        description=f"Research: {prompts.RESEARCH_TOPIC}",
        expected_output="A concise research summary.",
        agent=researcher,
        tools=[_search_tool, _read_page_tool],
    )
    review_task = Task(
        description=prompts.reviewer_prompt("<the research summary>"),
        expected_output="An APPROVED or CHANGES REQUESTED verdict.",
        agent=reviewer,
        context=[research_task],
        tools=[_read_page_tool],
    )

    return Crew(
        agents=[researcher, reviewer],
        tasks=[research_task, review_task],
        process=Process.sequential,
    )


def test_shared_workflow_produces_single_correctly_linked_trace(tracer):
    crew = _build_crew()
    adapter = CrewAIAdapter(tracer)
    adapter.instrument(crew)

    # Deliberately NOT wrapped in `with tracer.start_span(...)` -- see
    # module docstring for why that matters.
    result = crew.kickoff()
    assert prompts.FAKE_SUMMARY in str(result) or prompts.FAKE_REVIEW in str(result)

    spans = tracer._exporter.recorded
    assert spans, "expected the shared workflow to emit at least one span"

    # Milestone 3 test-strategy baseline: no orphans, one connected trace.
    assert_no_orphan_spans(spans)
    assert_single_trace(spans)

    # "review" (fixtures.WORKFLOW_STEPS' last step) is the one step of the
    # four whose canonical kind is `agent.handoff`, not `llm.call` -- see
    # fixtures.py. Assert structurally against that kind, not a literal
    # span name.
    review_kind = fixtures.WORKFLOW_STEPS[-1]["kind"]
    assert review_kind == SpanKind.AGENT_HANDOFF.value
    handoff_spans = [s for s in spans if s.kind == SpanKind.AGENT_HANDOFF]
    assert len(handoff_spans) >= 1
    assert any("Reviewer" in s.name for s in handoff_spans)
    # Both tasks (research, review) should have completed and produced a
    # handoff span -- one per delegation, matching this adapter's
    # preserved task_callback -> agent.handoff mapping.
    assert len(handoff_spans) == 2
    researcher_handoff = next(s for s in handoff_spans if "Researcher" in s.name)
    reviewer_handoff = next(s for s in handoff_spans if "Reviewer" in s.name)

    # Each of the researcher's 2 tool-decision turns (search, read_page)
    # and the reviewer's 1 tool-decision turn (re-reading the source
    # before approving) fires `step_callback` twice -- CrewAI's own
    # `AgentExecutor` invokes it once for the tool result and once for
    # the parsed `AgentAction` (see `agentmesh_crewai.adapter`'s module
    # docstring / `LIMITATIONS.md`: CrewAI's default executor does NOT
    # invoke `step_callback` at all for a task's terminal "Final Answer"
    # turn, only for intermediate tool-use turns -- confirmed against
    # `crewai.experimental.agent_executor.AgentExecutor`). What matters
    # for this adapter is that every one of these spans is parented to
    # *its own* task's handoff span -- not flattened to a single shared
    # parent the way the pre-SpanTracker implementation would (see
    # module docstring) -- which is exactly what the split below checks.
    llm_spans = [s for s in spans if s.kind == SpanKind.LLM_CALL]
    assert len(llm_spans) == 6
    parents = {s.parent_span_id for s in llm_spans}
    assert parents == {researcher_handoff.span_id, reviewer_handoff.span_id}
    researcher_steps = [s for s in llm_spans if s.parent_span_id == researcher_handoff.span_id]
    reviewer_steps = [s for s in llm_spans if s.parent_span_id == reviewer_handoff.span_id]
    assert len(researcher_steps) == 4
    assert len(reviewer_steps) == 2

    # Both handoff spans resolve to the same parent (the adapter's
    # synthetic, never-exported crew-level structural root), which has
    # no exported ancestor of its own -- so both are `None` here, not
    # each other's span_id.
    assert researcher_handoff.parent_span_id is None
    assert reviewer_handoff.parent_span_id is None
