"""Reusable "golden trace" test helpers for AgentMesh framework adapters.

Every adapter test suite (LangGraph, CrewAI, AutoGen, OpenAI Agents SDK,
and this package's own tests) needs the same three things: a fake
exporter to capture spans without a real Collector, and a couple of
shape assertions on the resulting flat span list — that it forms one
connected tree with no dangling parent references (Milestone 3's "no
orphan spans" acceptance criterion) and belongs to a single trace. These
were first written inline in `sdk/integrations/crewai/tests/test_crewai.py`
(`FakeExporter`) and are promoted here so every integration's test suite
imports one shared implementation instead of redefining it.
"""

from __future__ import annotations

from typing import Any, Dict, List

__all__ = [
    "FakeExporter",
    "build_span_tree",
    "assert_no_orphan_spans",
    "assert_single_trace",
]


class FakeExporter:
    """In-memory stand-in for `agentmesh.exporter.BatchingExporter`.

    Same shape as the real exporter's public surface (`.record(span)`,
    `.shutdown()`) so it can be passed directly to
    `agentmesh.Tracer(project_id=..., exporter=...)` in tests; recorded
    spans accumulate in `.recorded` in export order for assertions.
    """

    def __init__(self) -> None:
        self.recorded: List[Any] = []

    def record(self, span: Any) -> None:
        self.recorded.append(span)

    def shutdown(self) -> None:
        pass


def build_span_tree(spans: List[Any]) -> Dict[str, Dict[str, Any]]:
    """Build a nested `{span_id: {"span": span, "children": [...]}}` tree
    from a flat list of exported spans, for readable shape assertions
    (``tree[root_id]["children"][0]["span"].name == "..."``) instead of
    manually cross-referencing `parent_span_id` values by hand.

    A span is a root if it has no `parent_span_id`, or if its
    `parent_span_id` doesn't match any `span_id` present in ``spans``
    (e.g. the true root's parent lives outside the captured batch).
    """
    nodes: Dict[str, Dict[str, Any]] = {
        span.span_id: {"span": span, "children": []} for span in spans
    }
    known_ids = set(nodes)

    roots: Dict[str, Dict[str, Any]] = {}
    for span in spans:
        parent_id = span.parent_span_id
        if parent_id is not None and parent_id in known_ids:
            nodes[parent_id]["children"].append(nodes[span.span_id])
        else:
            roots[span.span_id] = nodes[span.span_id]
    return roots


def assert_no_orphan_spans(spans: List[Any]) -> None:
    """Assert every non-root span's `parent_span_id` resolves to another
    span's `span_id` within ``spans``.

    This is Milestone 3's test-strategy "no orphan spans" acceptance
    criterion: a span whose `parent_span_id` is set but points nowhere in
    the captured batch indicates a broken parent-chain link (e.g. a
    `SpanTracker` structural node incorrectly exported, or a real bug in
    an adapter's callback wiring) rather than a legitimate trace root.
    """
    known_ids = {span.span_id for span in spans}
    orphans = [
        span
        for span in spans
        if span.parent_span_id is not None and span.parent_span_id not in known_ids
    ]
    if orphans:
        details = ", ".join(f"{s.name!r} (span_id={s.span_id}, parent_span_id={s.parent_span_id})" for s in orphans)
        raise AssertionError(
            f"found {len(orphans)} orphan span(s) whose parent_span_id does not "
            f"resolve to any span_id in the given list: {details}"
        )


def assert_single_trace(spans: List[Any]) -> str:
    """Assert every span in ``spans`` shares one `trace_id`, and return it.

    A framework adapter should never split one logical run across
    multiple trace ids; catching that here is cheaper than debugging a
    trace that mysteriously shows up split across two rows in the Web
    Console's trace list.
    """
    if not spans:
        raise AssertionError("assert_single_trace() called with an empty span list")
    trace_ids = {span.trace_id for span in spans}
    if len(trace_ids) != 1:
        raise AssertionError(
            f"expected exactly one trace_id across {len(spans)} span(s), "
            f"found {len(trace_ids)}: {sorted(trace_ids)}"
        )
    return next(iter(trace_ids))
