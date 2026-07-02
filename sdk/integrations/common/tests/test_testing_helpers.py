import agentmesh
import pytest
from agentmesh_integrations_common.testing import (
    FakeExporter,
    assert_no_orphan_spans,
    assert_single_trace,
    build_span_tree,
)


def make_span(span_id, parent_span_id, trace_id="trace-1", name="span"):
    return agentmesh.Span(
        project_id="test-proj",
        kind=agentmesh.SpanKind.TOOL_CALL,
        name=name,
        trace_id=trace_id,
        span_id=span_id,
        parent_span_id=parent_span_id,
    )


def test_fake_exporter_records_in_order_and_shuts_down_cleanly():
    exporter = FakeExporter()
    s1 = make_span("s1", None)
    s2 = make_span("s2", "s1")
    exporter.record(s1)
    exporter.record(s2)
    assert exporter.recorded == [s1, s2]
    exporter.shutdown()  # must not raise


def test_build_span_tree_three_level_nesting():
    root = make_span("root", None, name="root")
    mid = make_span("mid", "root", name="mid")
    leaf = make_span("leaf", "mid", name="leaf")

    tree = build_span_tree([root, mid, leaf])

    assert set(tree.keys()) == {"root"}
    root_node = tree["root"]
    assert root_node["span"] is root
    assert len(root_node["children"]) == 1

    mid_node = root_node["children"][0]
    assert mid_node["span"] is mid
    assert len(mid_node["children"]) == 1

    leaf_node = mid_node["children"][0]
    assert leaf_node["span"] is leaf
    assert leaf_node["children"] == []


def test_build_span_tree_treats_missing_parent_as_a_root():
    dangling = make_span("child", "missing-parent", name="child")
    tree = build_span_tree([dangling])
    assert set(tree.keys()) == {"child"}
    assert tree["child"]["span"] is dangling


def test_build_span_tree_supports_multiple_roots_and_siblings():
    root_a = make_span("root_a", None, name="root_a")
    root_b = make_span("root_b", None, name="root_b")
    child_a1 = make_span("child_a1", "root_a", name="child_a1")
    child_a2 = make_span("child_a2", "root_a", name="child_a2")

    tree = build_span_tree([root_a, root_b, child_a1, child_a2])

    assert set(tree.keys()) == {"root_a", "root_b"}
    assert len(tree["root_a"]["children"]) == 2
    assert tree["root_b"]["children"] == []


def test_assert_no_orphan_spans_passes_for_well_formed_tree():
    root = make_span("root", None)
    child = make_span("child", "root")
    assert_no_orphan_spans([root, child])  # must not raise


def test_assert_no_orphan_spans_catches_genuine_orphan():
    root = make_span("root", None)
    orphan = make_span("orphan", "does-not-exist", name="orphan")
    with pytest.raises(AssertionError, match="orphan"):
        assert_no_orphan_spans([root, orphan])


def test_assert_single_trace_returns_the_shared_trace_id():
    s1 = make_span("s1", None, trace_id="trace-abc")
    s2 = make_span("s2", "s1", trace_id="trace-abc")
    assert assert_single_trace([s1, s2]) == "trace-abc"


def test_assert_single_trace_raises_on_mixed_trace_ids():
    s1 = make_span("s1", None, trace_id="trace-a")
    s2 = make_span("s2", None, trace_id="trace-b")
    with pytest.raises(AssertionError):
        assert_single_trace([s1, s2])


def test_assert_single_trace_raises_on_empty_list():
    with pytest.raises(AssertionError):
        assert_single_trace([])
