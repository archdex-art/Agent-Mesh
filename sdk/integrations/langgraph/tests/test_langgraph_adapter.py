"""Tests for `agentmesh_langgraph.adapter.LangGraphAdapter`.

Builds the Milestone 3 shared workflow (Search tool -> Read page tool ->
LLM summary -> Reviewer agent handoff, per `examples/shared/`) as a small
LangGraph graph, instruments it, runs it, and asserts the exported trace
against `agentmesh_integrations_common.testing`'s shared golden-trace
helpers rather than redefining them — per the Milestone 3 test-strategy
contract every adapter's suite follows.
"""

from __future__ import annotations

import pathlib
import sys
from typing import TypedDict

import pytest
from langgraph.graph import END, START, StateGraph

import agentmesh
from agentmesh_integrations_common import SpanKind, SpanStatus
from agentmesh_integrations_common.testing import (
    FakeExporter,
    assert_no_orphan_spans,
    assert_single_trace,
)
from agentmesh_langgraph import LangGraphAdapter, node_kind_metadata

# examples/shared is a plain module directory, not an installed package —
# see examples/shared/README.md's documented import pattern.
sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[4] / "examples" / "shared"))
import fixtures  # noqa: E402
import prompts  # noqa: E402
import tools  # noqa: E402


class WorkflowState(TypedDict, total=False):
    topic: str
    search_results: str
    notes: str
    summary: str
    review: str


def _search_node(state: WorkflowState) -> dict:
    return {"search_results": tools.search_tool(state["topic"])}


def _read_page_node(state: WorkflowState) -> dict:
    return {"notes": tools.read_page_tool(state["search_results"])}


def _summarize_node(state: WorkflowState) -> dict:
    prompts.summarizer_prompt(state["notes"])  # the (canned) LLM call site
    return {"summary": prompts.FAKE_SUMMARY}


def _review_node(state: WorkflowState) -> dict:
    prompts.reviewer_prompt(state["summary"])  # the reviewer-agent call site
    return {"review": prompts.FAKE_REVIEW}


def _build_shared_workflow_graph() -> StateGraph:
    """The shared workflow, node-for-node: search -> read_page -> summarize
    -> review, each tagged with its canonical AgentMesh kind via
    `node_kind_metadata()` (see `adapter.py`'s module docstring)."""
    graph: StateGraph = StateGraph(WorkflowState)
    graph.add_node("search", _search_node, metadata=node_kind_metadata(SpanKind.TOOL_CALL))
    graph.add_node("read_page", _read_page_node, metadata=node_kind_metadata(SpanKind.TOOL_CALL))
    graph.add_node("summarize", _summarize_node, metadata=node_kind_metadata(SpanKind.LLM_CALL))
    graph.add_node("review", _review_node, metadata=node_kind_metadata(SpanKind.AGENT_HANDOFF))
    graph.add_edge(START, "search")
    graph.add_edge("search", "read_page")
    graph.add_edge("read_page", "summarize")
    graph.add_edge("summarize", "review")
    graph.add_edge("review", END)
    return graph


@pytest.fixture
def fake_tracer():
    """A directly-constructed `agentmesh.Tracer` needs no
    `agentmesh.configure()`/`monkeypatch` dance — only code paths that go
    through the module-level default tracer do."""
    exporter = FakeExporter()
    return agentmesh.Tracer(project_id="test-proj", exporter=exporter), exporter


def test_shared_workflow_emits_expected_span_shape(fake_tracer):
    tracer, exporter = fake_tracer
    adapter = LangGraphAdapter(tracer)
    compiled = _build_shared_workflow_graph().compile()
    adapter.instrument(compiled)

    result = compiled.invoke({"topic": prompts.RESEARCH_TOPIC})
    assert result["review"] == prompts.FAKE_REVIEW

    spans = exporter.recorded
    assert_single_trace(spans)
    assert_no_orphan_spans(spans)

    # Kinds/order/count only, per fixtures.py's documented contract — never
    # literal span names, which are each framework adapter's own call.
    expected_kinds = [SpanKind(step["kind"]) for step in fixtures.WORKFLOW_STEPS]
    assert [span.kind for span in spans] == expected_kinds
    assert len(spans) == len(fixtures.expected_step_names())

    # The one handoff is the reviewer step (Architecture.md §3 / fixtures.py).
    handoff = spans[-1]
    assert handoff.kind == SpanKind.AGENT_HANDOFF
    assert handoff.status == SpanStatus.OK
    # A node's recorded `output` is its full state-diff, JSON-serialized
    # (matching `adapter._stringify`), not just the canned reviewer text
    # in isolation — but it must still be present and readable in it.
    assert prompts.FAKE_REVIEW in handoff.output

    # True linear parent chain: each step's parent is the previous step's
    # span, and the first step is a trace root (its structural graph-root
    # parent is never exported, so its own parent_span_id is None).
    assert spans[0].parent_span_id is None
    for parent, child in zip(spans, spans[1:]):
        assert child.parent_span_id == parent.span_id

    # Every exported span is tagged with which framework produced it, per
    # `agentmesh_integrations_common.spans.FRAMEWORK_ATTR`'s convention.
    assert all(span.attributes.get("framework") == "langgraph" for span in spans)


def test_shared_workflow_via_uncompiled_state_graph(fake_tracer):
    """`instrument()` also accepts a `StateGraph` before `.compile()`,
    wrapping whatever compiled graph `.compile()` later produces."""
    tracer, exporter = fake_tracer
    adapter = LangGraphAdapter(tracer)
    state_graph = _build_shared_workflow_graph()
    adapter.instrument(state_graph)

    compiled = state_graph.compile()
    compiled.invoke({"topic": prompts.RESEARCH_TOPIC})

    spans = exporter.recorded
    assert_single_trace(spans)
    assert_no_orphan_spans(spans)
    expected_kinds = [SpanKind(step["kind"]) for step in fixtures.WORKFLOW_STEPS]
    assert [span.kind for span in spans] == expected_kinds


def test_shared_workflow_via_stream(fake_tracer):
    """`instrument()` wraps `.stream()` (and `.ainvoke()`/`.astream()`)
    identically to `.invoke()` — exercised here for `.stream()`."""
    tracer, exporter = fake_tracer
    adapter = LangGraphAdapter(tracer)
    compiled = _build_shared_workflow_graph().compile()
    adapter.instrument(compiled)

    chunks = list(compiled.stream({"topic": prompts.RESEARCH_TOPIC}))
    assert len(chunks) == 4  # one chunk per node

    spans = exporter.recorded
    assert_single_trace(spans)
    assert_no_orphan_spans(spans)
    expected_kinds = [SpanKind(step["kind"]) for step in fixtures.WORKFLOW_STEPS]
    assert [span.kind for span in spans] == expected_kinds


def test_untagged_node_is_structural_and_collapses_out_of_parent_chain(fake_tracer):
    """A node with no `agentmesh_kind` metadata (e.g. a routing node) is
    tracked for parent-chain purposes but never exported as a span; a real
    span downstream of it still resolves its parent to the nearest
    *exported* ancestor, per `SpanTracker`'s documented contract."""
    tracer, exporter = fake_tracer
    adapter = LangGraphAdapter(tracer)

    class State(TypedDict, total=False):
        value: str

    def node_a(state: State) -> dict:
        return {"value": "a"}

    def route(state: State) -> dict:  # structural: no workflow-visible action
        return {}

    def node_c(state: State) -> dict:
        return {"value": state.get("value", "") + "c"}

    graph: StateGraph = StateGraph(State)
    graph.add_node("a", node_a, metadata=node_kind_metadata(SpanKind.TOOL_CALL))
    graph.add_node("route", route)  # untagged -> kind=None
    graph.add_node("c", node_c, metadata=node_kind_metadata(SpanKind.LLM_CALL))
    graph.add_edge(START, "a")
    graph.add_edge("a", "route")
    graph.add_edge("route", "c")
    graph.add_edge("c", END)
    compiled = graph.compile()
    adapter.instrument(compiled)

    compiled.invoke({})

    spans = exporter.recorded
    assert [span.name for span in spans] == ["a", "c"]
    assert_no_orphan_spans(spans)
    assert_single_trace(spans)
    # "route" never becomes its own span, but "c" still parents correctly
    # onto "a" by walking past it.
    assert spans[1].parent_span_id == spans[0].span_id


def test_node_error_marks_span_failed_and_still_finishes(fake_tracer):
    tracer, exporter = fake_tracer
    adapter = LangGraphAdapter(tracer)

    class State(TypedDict, total=False):
        value: str

    def failing_node(state: State) -> dict:
        raise RuntimeError("boom")

    graph: StateGraph = StateGraph(State)
    graph.add_node("search", failing_node, metadata=node_kind_metadata(SpanKind.TOOL_CALL))
    graph.add_edge(START, "search")
    graph.add_edge("search", END)
    compiled = graph.compile()
    adapter.instrument(compiled)

    with pytest.raises(RuntimeError, match="boom"):
        compiled.invoke({})

    spans = exporter.recorded
    assert len(spans) == 1
    assert spans[0].status == SpanStatus.ERROR
    assert "boom" in spans[0].output
    # No bookkeeping leak after an aborted run.
    assert adapter._chain_tip == {}
    assert adapter._node_root == {}
    assert adapter._pending == {}


def test_concurrent_invocations_stay_in_separate_traces(fake_tracer):
    """One adapter instance instruments one graph but is invoked twice in
    a row; each invocation must land in its own single-trace span set with
    no cross-talk in the adapter's bookkeeping."""
    tracer, exporter = fake_tracer
    adapter = LangGraphAdapter(tracer)
    compiled = _build_shared_workflow_graph().compile()
    adapter.instrument(compiled)

    compiled.invoke({"topic": prompts.RESEARCH_TOPIC})
    compiled.invoke({"topic": prompts.RESEARCH_TOPIC})

    spans = exporter.recorded
    assert len(spans) == 8
    trace_ids = {span.trace_id for span in spans}
    assert len(trace_ids) == 2
    for trace_id in trace_ids:
        subset = [span for span in spans if span.trace_id == trace_id]
        assert_single_trace(subset)
        assert_no_orphan_spans(subset)
        expected_kinds = [SpanKind(step["kind"]) for step in fixtures.WORKFLOW_STEPS]
        assert [span.kind for span in subset] == expected_kinds
    assert adapter._chain_tip == {}
    assert adapter._node_root == {}
    assert adapter._pending == {}


def test_node_kind_metadata_helper():
    assert node_kind_metadata(SpanKind.TOOL_CALL) == {"agentmesh_kind": "tool.call"}
    assert node_kind_metadata("agent.handoff") == {"agentmesh_kind": "agent.handoff"}
    assert node_kind_metadata() == {}
    assert node_kind_metadata(None, owner="team-x") == {"owner": "team-x"}
