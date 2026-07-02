"""CrewAI reference implementation of the Milestone 3 shared workflow:

    Research topic -> Search tool -> Read page tool -> LLM summary
        -> Reviewer agent -> Return answer

See `examples/shared/README.md` for why every Milestone 3 example app
(LangGraph, CrewAI, AutoGen, OpenAI Agents SDK) runs this exact same
workflow with the exact same canned content: it is what makes the four
resulting traces directly comparable in the Console.

A "Researcher" agent performs the search + read_page steps (using the
shared, deterministic `search_tool`/`read_page_tool` functions wired in
as CrewAI tools) and produces the summary; a "Reviewer" agent (CrewAI's
closest concept to a sub-agent handoff -- see
`agentmesh_crewai.adapter`'s module docstring and
`sdk/integrations/common/agentmesh_integrations_common/mapper.py`'s
mapping table) reviews it and returns the final verdict. Both agents use
a scripted `CannedLLM` (no network calls, no API key) that returns
canned ReAct-format responses, matching the pattern already used in
`sdk/integrations/crewai/tests/test_crewai.py` and
`tests/test_shared_workflow.py`.
"""

from __future__ import annotations

import pathlib
import sys

from crewai import Agent, Crew, Process, Task
from crewai.llm import LLM
from crewai.tools import tool
from pydantic import PrivateAttr

import agentmesh
from agentmesh_crewai import CrewAIAdapter

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent / "shared"))

import prompts  # noqa: E402  (examples/shared, path-inserted above)
import tools as shared_tools  # noqa: E402


@tool("search_tool")
def search_tool(query: str) -> str:
    """Search the AgentMesh fixture knowledge base for a topic."""
    print(f"  [search_tool] query={query!r}")
    return shared_tools.search_tool(query)


@tool("read_page_tool")
def read_page_tool(url_or_ref: str) -> str:
    """Read the full canned content of a previously-found page or reference."""
    print(f"  [read_page_tool] url_or_ref={url_or_ref!r}")
    return shared_tools.read_page_tool(url_or_ref)


class CannedLLM(LLM):
    """Returns each of `responses` in order across successive `.call()`
    invocations (then repeats the last one) -- scripted so this example
    needs neither a real model nor an API key, matching
    `examples/shared/README.md`'s `CannedLLM`/`FakeLLM` convention.
    """

    _responses: list[str] = PrivateAttr(default_factory=list)
    _index: int = PrivateAttr(default=0)

    def __init__(self, *, model: str, responses: list[str], **kwargs):
        super().__init__(model=model, **kwargs)
        self._responses = responses

    def call(self, messages, *args, **kwargs):
        i = min(self._index, len(self._responses) - 1)
        self._index += 1
        return self._responses[i]


project_id = "fe375e79-b7aa-4bd5-a7e6-72ad6db48d5b"
api_key = "am_live_integtest796a7534ce"

tracer = agentmesh.configure(project_id=project_id, api_key=api_key, endpoint="localhost:4317")

researcher_llm = CannedLLM(
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
reviewer_llm = CannedLLM(
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
    tools=[search_tool, read_page_tool],
)
reviewer = Agent(
    role="Reviewer",
    goal="Review a research summary for accuracy and completeness.",
    backstory="You are a careful reviewer agent.",
    llm=reviewer_llm,
    tools=[read_page_tool],
)

research_task = Task(
    description=f"Research: {prompts.RESEARCH_TOPIC}",
    expected_output="A concise research summary.",
    agent=researcher,
    tools=[search_tool, read_page_tool],
)
review_task = Task(
    description=prompts.reviewer_prompt("<the research summary>"),
    expected_output="An APPROVED or CHANGES REQUESTED verdict.",
    agent=reviewer,
    context=[research_task],
    tools=[read_page_tool],
)

crew = Crew(
    agents=[researcher, reviewer],
    tasks=[research_task, review_task],
    process=Process.sequential,
)

adapter = CrewAIAdapter(tracer)
adapter.instrument(crew)

print("=== Milestone 3 shared workflow (CrewAI) ===")
print(f"Research topic: {prompts.RESEARCH_TOPIC}")
print()

try:
    result = crew.kickoff()
except Exception as e:
    print("crew failed:", e)
    result = None

print()
print("--- Logical steps ---")
print("1. search    : Researcher agent called search_tool (see [search_tool] line above)")
print("2. read_page : Researcher agent called read_page_tool (see [read_page_tool] line above)")
print("3. summarize :", research_task.output.raw if research_task.output else "<no output>")
print("4. review    :", review_task.output.raw if review_task.output else "<no output>")
print()
print("Final answer:", result)

tracer.shutdown()
print("Trace ID:", adapter.trace_id)
