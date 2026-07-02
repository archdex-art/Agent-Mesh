"""LangGraph reference example for AgentMesh Milestone 3.

Runs the shared workflow (examples/shared/) as a small LangGraph graph —
search -> read_page -> summarize -> review — instrumented with
`agentmesh_langgraph.LangGraphAdapter`, then prints the resulting
trace_id and a step-by-step summary of every span the run emitted.

How to run
----------
From the repo root (recommended, matches every other example app)::

    python examples/langgraph-support-bot/main.py

Or from anywhere else — this script resolves `examples/shared` and its
own dependencies relative to its own file location, not the current
working directory, so `python /abs/path/to/main.py` also works as long
as `agentmesh-langgraph` (and its `agentmesh-sdk`/
`agentmesh-integrations-common`/`langgraph` dependencies) are installed,
e.g. via::

    cd sdk/integrations/langgraph && pip install -e . -e ../common -e ../../python

No network access and no API key are required: this demo uses the same
canned tools/prompts/outputs every Milestone 3 example app uses (see
`examples/shared/`), and records spans locally instead of actually
delivering them to a Collector (contrast with
`examples/crewai-research-crew/main.py`'s `agentmesh.configure(...)`
against an unlistened `localhost:4317` — equally valid, but this demo
prefers a local recording exporter so it can print exactly what was
captured without depending on gRPC's graceful-failure path).
"""

from __future__ import annotations

import pathlib
import sys
from typing import Any, TypedDict

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent / "shared"))
import prompts  # noqa: E402
import tools  # noqa: E402

import agentmesh
from agentmesh_langgraph import LangGraphAdapter, node_kind_metadata
from langgraph.graph import END, START, StateGraph


class RecordingExporter:
    """Local stand-in for `agentmesh.exporter.BatchingExporter`: same
    `record(span)`/`shutdown()` shape a real `Tracer` expects, but keeps
    every exported span in memory instead of sending it over gRPC — this
    demo has nothing listening on a Collector endpoint, and would rather
    print exactly what it captured than rely on the exporter's
    graceful-degradation-on-network-failure path (Architecture.md §17)
    to merely avoid crashing.
    """

    def __init__(self) -> None:
        self.recorded: list[Any] = []

    def record(self, span: Any) -> None:
        self.recorded.append(span)

    def shutdown(self) -> None:
        pass


class SupportBotState(TypedDict, total=False):
    topic: str
    search_results: str
    notes: str
    summary: str
    review: str


def search_node(state: SupportBotState) -> dict:
    return {"search_results": tools.search_tool(state["topic"])}


def read_page_node(state: SupportBotState) -> dict:
    return {"notes": tools.read_page_tool(state["search_results"])}


def summarize_node(state: SupportBotState) -> dict:
    # A real integration would hand this prompt to an LLM; this demo (like
    # every Milestone 3 example app) uses the shared canned output instead
    # so every framework's trace carries identical, comparable content.
    prompts.summarizer_prompt(state["notes"])
    return {"summary": prompts.FAKE_SUMMARY}


def review_node(state: SupportBotState) -> dict:
    prompts.reviewer_prompt(state["summary"])
    return {"review": prompts.FAKE_REVIEW}


def build_graph() -> StateGraph:
    """The shared Milestone 3 workflow — Research topic -> Search tool ->
    Read page tool -> LLM summary -> Reviewer agent -> Return answer — as
    a LangGraph graph. Each node is tagged with its canonical AgentMesh
    span kind via `node_kind_metadata()`; the transition into "review" is
    the one `agent.handoff`, matching `examples/shared/fixtures.py`'s
    `WORKFLOW_STEPS`.
    """
    graph: StateGraph = StateGraph(SupportBotState)
    graph.add_node("search", search_node, metadata=node_kind_metadata(agentmesh.SpanKind.TOOL_CALL))
    graph.add_node("read_page", read_page_node, metadata=node_kind_metadata(agentmesh.SpanKind.TOOL_CALL))
    graph.add_node("summarize", summarize_node, metadata=node_kind_metadata(agentmesh.SpanKind.LLM_CALL))
    graph.add_node("review", review_node, metadata=node_kind_metadata(agentmesh.SpanKind.AGENT_HANDOFF))
    graph.add_edge(START, "search")
    graph.add_edge("search", "read_page")
    graph.add_edge("read_page", "summarize")
    graph.add_edge("summarize", "review")
    graph.add_edge("review", END)
    return graph


def main() -> None:
    project_id = "fe375e79-b7aa-4bd5-a7e6-72ad6db48d5b"
    exporter = RecordingExporter()
    tracer = agentmesh.Tracer(project_id=project_id, exporter=exporter)

    adapter = LangGraphAdapter(tracer)
    compiled = build_graph().compile()
    adapter.instrument(compiled)

    result = compiled.invoke({"topic": prompts.RESEARCH_TOPIC})

    spans = exporter.recorded
    trace_id = spans[0].trace_id if spans else None

    print(f"Trace ID: {trace_id}")
    print(f"Final answer: {result['review']}")
    print()
    print(f"Emitted {len(spans)} workflow span(s) "
          "(plus 1 structural bookkeeping entry for the graph invocation "
          "itself, tracked for parent-chain linkage but never exported, "
          "per agentmesh_integrations_common.context.SpanTracker's "
          "kind=None convention):")
    for index, span in enumerate(spans, start=1):
        parent = span.parent_span_id or "(trace root)"
        print(f"  {index}. [{span.kind.value:14s}] {span.name:12s} "
              f"status={span.status.value:5s} parent={parent}")

    tracer.shutdown()


if __name__ == "__main__":
    main()
