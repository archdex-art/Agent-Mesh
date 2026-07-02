import agentmesh
import pytest
from agentmesh_integrations_common.adapter import FrameworkAdapter
from agentmesh_integrations_common.context import SpanTracker, UnknownSpanError
from agentmesh_integrations_common.mapper import map_or_none
from agentmesh_integrations_common.spans import set_attrs
from agentmesh_integrations_common.testing import (
    FakeExporter,
    assert_no_orphan_spans,
    assert_single_trace,
)


@pytest.fixture
def tracer():
    return agentmesh.Tracer(project_id="test-proj", exporter=FakeExporter())


def test_nested_structural_and_real_spans_produce_correct_parent_linkage(tracer):
    """A structural (kind=None) root must never be exported, but a real
    span two levels below it must still resolve its parent correctly:
    skipping the un-exported structural node entirely (parent_span_id is
    None) rather than pointing at a span_id that was never recorded."""
    tracker = SpanTracker(tracer)

    tracker.start("root", None, "graph-run")
    tracker.start("child_llm", agentmesh.SpanKind.LLM_CALL, "call-model", parent_external_id="root")
    tracker.start(
        "grandchild_tool",
        agentmesh.SpanKind.TOOL_CALL,
        "search",
        parent_external_id="child_llm",
    )

    # All three share one trace, including the never-exported structural node.
    trace_id = tracker.active_trace_id("root")
    assert trace_id is not None
    assert tracker.active_trace_id("child_llm") == trace_id
    assert tracker.active_trace_id("grandchild_tool") == trace_id

    tracker.finish("grandchild_tool", status=agentmesh.SpanStatus.OK, output="results")
    tracker.finish("child_llm", status=agentmesh.SpanStatus.OK, output="answer")
    tracker.finish("root")  # structural: no-op export, must not raise

    spans = tracer._exporter.recorded
    assert len(spans) == 2  # the structural root is never exported

    by_name = {s.name: s for s in spans}
    llm_span = by_name["call-model"]
    tool_span = by_name["search"]

    assert llm_span.parent_span_id is None  # root was structural, so no exported parent
    assert tool_span.parent_span_id == llm_span.span_id

    assert_single_trace(spans)
    assert_no_orphan_spans(spans)


def test_deep_structural_chain_collapses_to_nearest_exported_ancestor(tracer):
    """Multiple consecutive structural nodes must all resolve down to the
    same nearest exported ancestor, not just the immediate parent."""
    tracker = SpanTracker(tracer)

    tracker.start("s1", None, "structural-1")
    tracker.start("real1", agentmesh.SpanKind.AGENT_HANDOFF, "handoff", parent_external_id="s1")
    tracker.start("s2", None, "structural-2", parent_external_id="real1")
    tracker.start("s3", None, "structural-3", parent_external_id="s2")
    tracker.start("real2", agentmesh.SpanKind.TOOL_CALL, "tool", parent_external_id="s3")

    tracker.finish("real2")
    tracker.finish("real1")
    tracker.finish("s3")
    tracker.finish("s2")
    tracker.finish("s1")

    spans = tracer._exporter.recorded
    by_name = {s.name: s for s in spans}
    assert by_name["tool"].parent_span_id == by_name["handoff"].span_id
    assert_no_orphan_spans(spans)
    assert_single_trace(spans)


def test_finish_unknown_external_id_raises(tracer):
    tracker = SpanTracker(tracer)
    with pytest.raises(UnknownSpanError):
        tracker.finish("does-not-exist")


def test_finish_removes_entry_so_double_finish_raises(tracer):
    tracker = SpanTracker(tracer)
    tracker.start("s1", agentmesh.SpanKind.LLM_CALL, "call")
    tracker.finish("s1")
    with pytest.raises(UnknownSpanError):
        tracker.finish("s1")


def test_map_or_none_translates_framework_events_to_span_kinds():
    mapping = {
        "llm_node": agentmesh.SpanKind.LLM_CALL,
        "tool_node": agentmesh.SpanKind.TOOL_CALL,
        "router_node": None,
    }
    assert map_or_none("llm_node", mapping) == agentmesh.SpanKind.LLM_CALL
    assert map_or_none("router_node", mapping) is None
    assert map_or_none("totally_unknown_node", mapping) is None


def test_set_attrs_stringifies_and_skips_none(tracer):
    tracker = SpanTracker(tracer)
    tracker.start(
        "span1",
        agentmesh.SpanKind.TOOL_CALL,
        "search",
        attributes={"framework": "langgraph", "retries": "0"},
    )
    tracker.finish("span1")
    span = tracer._exporter.recorded[0]
    assert span.attributes["framework"] == "langgraph"

    set_attrs(span, extra=42, skipped=None)
    assert span.attributes["extra"] == "42"
    assert "skipped" not in span.attributes


def test_framework_adapter_shares_tracer_and_span_tracker(tracer):
    class DummyAdapter(FrameworkAdapter):
        def instrument(self, target):
            self.instrumented = target

    adapter = DummyAdapter(tracer)
    assert adapter.tracer is tracer
    assert isinstance(adapter.spans, SpanTracker)

    adapter.instrument("some-target")
    assert adapter.instrumented == "some-target"


def test_framework_adapter_cannot_be_instantiated_directly(tracer):
    with pytest.raises(TypeError):
        FrameworkAdapter(tracer)
