"""Shared span-model conventions for AgentMesh framework adapters.

AgentMesh's canonical span model (Architecture.md §3, mirrored in
`shared/span/span.go` and `agentmesh._span.SpanKind`) is FIXED at exactly
four kinds: `llm.call`, `tool.call`, `agent.handoff`, and `mcp.call`. The
last of these (`MCP_CALL`) is emitted only by the MCP Gateway, never by an
SDK adapter — every adapter built on top of this package (LangGraph,
CrewAI, AutoGen, OpenAI Agents SDK, ...) may only ever emit `LLM_CALL`,
`TOOL_CALL`, or `AGENT_HANDOFF` spans.

This is a closed set on purpose: it is what lets the Query API, the Web
Console's trace DAG viewer, the Cost Engine, and the Anomaly Detector
reason about a trace without knowing which framework produced it. Any
framework-specific detail that does not map cleanly onto one of the three
adapter-usable kinds — a LangGraph node type, a CrewAI task phase, an
AutoGen message role, a "retriever" or "memory" operation, a workflow
phase name, etc. — MUST be encoded as a string entry in the span's
open-ended `attributes: dict[str, str]` field. It must NEVER become a new
top-level `SpanKind` member; adding kinds defeats the entire point of a
shared model that every downstream consumer can rely on staying small and
stable.

Example: a LangGraph node that runs a "reducer" step which is neither an
LLM call nor a tool call is not exported as a span at all (see
`context.SpanTracker`'s `kind=None` "structural" node concept) — but if an
adapter *does* export it as, say, a `TOOL_CALL`, the fact that it was a
"reducer" node belongs in
`span.attributes[FRAMEWORK_ATTR] = "langgraph"` plus a
framework-specific key such as `span.attributes["langgraph.node_type"]`,
not in a new `SpanKind`.
"""

from __future__ import annotations

from agentmesh import SpanKind, SpanStatus

# Every adapter should tag the spans it produces with which framework
# emitted them, using this attribute key, so a mixed trace (e.g. a
# LangGraph graph that internally calls a CrewAI crew) can still be
# disambiguated in the Web Console without inventing a new SpanKind per
# framework.
FRAMEWORK_ATTR = "framework"

__all__ = ["SpanKind", "SpanStatus", "FRAMEWORK_ATTR", "set_attrs"]


def set_attrs(span, **attrs: object) -> None:
    """Stringify and assign multiple attributes onto ``span.attributes``.

    Convenience for the common adapter pattern of setting several
    framework-specific attributes at once, e.g.::

        set_attrs(span, **{FRAMEWORK_ATTR: "langgraph", "langgraph.node": node_name})

    Values are converted with ``str()`` to satisfy the `dict[str, str]`
    contract of `Span.attributes` (Architecture.md §3 / otlp-mapping.md).
    Keys whose value is ``None`` are skipped rather than stored as the
    literal string ``"None"``, since "attribute absent" and "attribute
    present but empty" are different things an adapter may want to
    express deliberately.
    """
    for key, value in attrs.items():
        if value is None:
            continue
        span.attributes[key] = str(value)
