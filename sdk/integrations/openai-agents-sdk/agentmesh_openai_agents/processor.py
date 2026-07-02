"""`agents.tracing.TracingProcessor` that mirrors OpenAI Agents SDK spans
onto AgentMesh.

The OpenAI Agents SDK's own tracing model (`agents.tracing`) nests spans as
a tree keyed by ``span.span_id``/``span.parent_id``, discriminated by
``span.span_data.type`` — a plain string ("agent", "task", "turn",
"generation", "function", "handoff", ...), *not* by dedicated per-kind
`Span` subclasses. As installed in this environment (`openai-agents`
0.17.x) the SDK's own `Runner` loop emits, per turn:

    task (workflow root) -> agent (current agent) -> turn (one model
    round-trip) -> generation | function | handoff (leaves)

Per Architecture.md §3's fixed-3-kind adapter model, only the leaves map
onto an AgentMesh `SpanKind`:

    | `span_data.type` | -> | AgentMesh `SpanKind` |
    |-------------------|----|----------------------|
    | `"generation"`    | -> | `LLM_CALL`            |
    | `"function"`      | -> | `TOOL_CALL`           |
    | `"handoff"`       | -> | `AGENT_HANDOFF`       |

`"task"`, `"turn"`, and `"agent"` are structural grouping spans with no
AgentMesh-kind equivalent — they are still tracked (via `kind=None`) so
real descendants resolve to the correct exported ancestor, but are never
exported themselves. Anything else the SDK ever adds (`"guardrail"`,
`"response"`, `"speech"`, `"custom"`, ...) falls back to the same
structural treatment via `mapper.map_or_none`'s `default=None`, so a
future SDK span type never gets force-mapped into the wrong kind.

Parent/child linkage across this asynchronous start/end callback pair
(`on_span_start` / `on_span_end` are two separate calls with no Python
call-stack relationship between them) is handled by
`agentmesh_integrations_common.context.SpanTracker`, keyed by the SDK's
own `span.span_id`, replacing this package's earlier hand-rolled
``self._am_spans: dict[str, Span] = {}`` bookkeeping.

Handoff span special case
--------------------------
`agents.tracing.handoff_span(from_agent=...)` is created with only
`from_agent` known; `to_agent` is populated by the SDK *inside* the
`with` block, i.e. it is not yet set when `on_span_start` fires.
`SpanTracker.start()` fixes a span's `name` at start time with no public
way to revise it before `finish()` (see the shared package's own
documented limitation). Since handoff spans are leaves in this SDK's
tracing model — no child span nests inside a `handoff` span — it is safe
to defer *both* `SpanTracker.start()` and `.finish()` for handoff spans
until `on_span_end`, once `to_agent` is known, and construct the correct
`handoff_to_<to_agent>` name in one shot. Every other kind (`"generation"`,
`"function"`, and all structural kinds) starts normally in
`on_span_start`, since their identifying field (`model` / `name`) is
already present at span creation.

Design note: `FrameworkAdapter` vs. plain composition
-------------------------------------------------------
`agentmesh_integrations_common.adapter.FrameworkAdapter` is an ABC whose
single abstract method, `instrument(self, target)`, models "wire this
adapter onto a framework object you were handed" (a graph, a crew, a
group chat, ...). The OpenAI Agents SDK's tracing hook does not fit that
shape: a `TracingProcessor` is not pointed at a specific target object —
it is registered globally via `agents.tracing.set_trace_processors([...])`
and then receives every trace's spans for the life of the process. There
is no natural `target` to hand `instrument()`. Rather than force an
awkward `instrument(self, target=None)` no-op onto the class purely to
satisfy the ABC, `AgentMeshTracingProcessor` stays `TracingProcessor`-only
and holds `self.spans = SpanTracker(tracer)` directly — exactly the
attribute `FrameworkAdapter.__init__` would have set, just without the
mismatched `instrument()` contract.

Usage::

    import agentmesh
    from agentmesh_openai_agents import AgentMeshTracingProcessor
    from agents.tracing import set_trace_processors

    tracer = agentmesh.configure(project_id=..., api_key=..., endpoint=...)
    set_trace_processors([AgentMeshTracingProcessor(tracer)])

See `LIMITATIONS.md` for the remaining known gaps (span input on
in-flight spans, and the discrepancy this retrofit found and fixed
between the SDK version this package originally assumed and the one
actually installed).
"""

import json
import logging
from typing import Any

import agentmesh
from agentmesh_integrations_common.context import SpanTracker
from agentmesh_integrations_common.mapper import map_or_none
from agentmesh_integrations_common.spans import FRAMEWORK_ATTR, SpanKind, SpanStatus
from agents.tracing import Span as OpenAISpan, TracingProcessor, Trace

logger = logging.getLogger("agentmesh_openai_agents")

_FRAMEWORK = "openai-agents-sdk"

# `span_data.type` -> AgentMesh SpanKind, for the 3 adapter-usable kinds.
# Anything not listed here (including every structural kind: "task",
# "turn", "agent") maps to `None` via `map_or_none`'s default.
_KIND_BY_SPAN_DATA_TYPE: dict[str, SpanKind] = {
    "generation": SpanKind.LLM_CALL,
    "function": SpanKind.TOOL_CALL,
    "handoff": SpanKind.AGENT_HANDOFF,
}


def _kind_for(span_data: Any) -> SpanKind | None:
    return map_or_none(getattr(span_data, "type", None), _KIND_BY_SPAN_DATA_TYPE)


def _name_for(span_data: Any, kind: SpanKind | None) -> str:
    if kind is SpanKind.LLM_CALL:
        return getattr(span_data, "model", None) or "unknown-model"
    if kind is SpanKind.TOOL_CALL:
        return getattr(span_data, "name", None) or "unknown-tool"
    if kind is SpanKind.AGENT_HANDOFF:
        to_agent = getattr(span_data, "to_agent", None) or "unknown-agent"
        return f"handoff_to_{to_agent}"
    # Structural span (kind=None): never exported, name is only used for
    # SpanTracker's own bookkeeping/debugging.
    return getattr(span_data, "name", None) or type(span_data).__name__


def _serialize(value: Any) -> str | None:
    if value is None:
        return None
    try:
        return json.dumps(value, default=str)
    except (TypeError, ValueError):
        return str(value)


class AgentMeshTracingProcessor(TracingProcessor):
    """Mirrors OpenAI Agents SDK spans onto an `agentmesh.Tracer`.

    Register via ``agents.tracing.set_trace_processors([AgentMeshTracingProcessor(tracer)])``.
    See the module docstring for the `span_data.type` -> `SpanKind` mapping
    and the handoff-span start/finish deferral this class relies on.
    """

    def __init__(self, tracer: "agentmesh.Tracer") -> None:
        self.tracer = tracer
        self.spans = SpanTracker(tracer)

    def on_trace_start(self, trace: Trace) -> None:  # noqa: D102 - see TracingProcessor
        # AgentMesh has no first-class "trace started" event distinct from
        # its first span; trace identity is derived from the span tree via
        # SpanTracker instead. Nothing to do here.
        pass

    def on_trace_end(self, trace: Trace) -> None:  # noqa: D102 - see TracingProcessor
        pass

    def on_span_start(self, span: "OpenAISpan[Any]") -> None:
        span_data = span.span_data
        kind = _kind_for(span_data)

        if kind is SpanKind.AGENT_HANDOFF:
            # `to_agent` isn't populated yet; defer to on_span_end (see
            # module docstring's "Handoff span special case").
            return

        name = _name_for(span_data, kind)
        attributes = {FRAMEWORK_ATTR: _FRAMEWORK}
        input_value = getattr(span_data, "input", None)
        if input_value is not None:
            attributes["input"] = _serialize(input_value)

        self.spans.start(
            span.span_id,
            kind,
            name,
            parent_external_id=span.parent_id,
            attributes=attributes,
        )

    def on_span_end(self, span: "OpenAISpan[Any]") -> None:
        span_data = span.span_data
        kind = _kind_for(span_data)
        error = span.error
        status = SpanStatus.ERROR if error is not None else SpanStatus.OK
        output = _serialize(error) if error is not None else _serialize(getattr(span_data, "output", None))

        if kind is SpanKind.AGENT_HANDOFF:
            name = _name_for(span_data, kind)
            attributes = {
                FRAMEWORK_ATTR: _FRAMEWORK,
                "from_agent": getattr(span_data, "from_agent", None),
                "to_agent": getattr(span_data, "to_agent", None),
            }
            self.spans.start(
                span.span_id,
                kind,
                name,
                parent_external_id=span.parent_id,
                attributes=attributes,
            )

        self.spans.finish(span.span_id, status=status, output=output)

    def shutdown(self) -> None:
        # SpanTracker has no queued/buffered state of its own to flush;
        # the underlying tracer's exporter owns that lifecycle.
        pass

    def force_flush(self) -> None:
        pass
