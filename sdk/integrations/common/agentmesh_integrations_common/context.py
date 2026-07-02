"""Parent-chain tracking for event-based (non-context-manager) frameworks.

`agentmesh.Tracer.start_span()` is a context manager: it links parent and
child spans automatically via an internal stack, but that only works when
spans nest as synchronous Python calls (`with tracer.start_span(...):`).
Several Milestone 3 target frameworks instead fire a *start* callback and
a separate, later *end* callback with no call-stack relationship between
them — LangGraph's per-node hooks, AutoGen's message hooks, and the
OpenAI Agents SDK's `TracingProcessor.on_span_start`/`on_span_end` all
work this way. `SpanTracker` is the shared abstraction that replaces the
ad hoc `dict[str, Span]`-keyed bookkeeping those integrations would
otherwise each hand-roll (see
`sdk/integrations/openai-agents-sdk/agentmesh_openai_agents/processor.py`'s
`on_span_start`/`on_span_end`, which duplicated exactly this pattern
before this module existed to share it).

The key piece of logic is what happens when a framework event does not
map onto one of AgentMesh's three adapter-usable span kinds
(`llm.call`/`tool.call`/`agent.handoff` — Architecture.md §3's
fixed-4-kind model; `mcp.call` is gateway-only). Rather than force such
"structural" events (a LangGraph graph's routing node, an OpenAI Agents
SDK `agent_span`/`task_span` grouping span, ...) into an arbitrary kind,
callers pass ``kind=None`` to `SpanTracker.start()`. The tracker still
records the event — so real descendants can find the correct nearest
*exported* ancestor to link to as `parent_span_id` — but never turns it
into an exported `Span`. This is what lets, e.g., a `tool.call` nested
three structural layers deep in a framework's native call tree still end
up with the right parent in the exported trace.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Dict, Optional, Union

from agentmesh import SpanStatus, Tracer
from agentmesh._span import Span, SpanKind, _new_span_id, _new_trace_id

from .spans import set_attrs

__all__ = ["SpanTracker", "UnknownSpanError"]


class UnknownSpanError(KeyError):
    """Raised when a `SpanTracker` lookup is given an `external_id` that
    was never `start()`-ed (or was already `finish()`-ed).

    Subclasses `KeyError` (the natural error for a missing dict key) but
    gives it a purpose-specific name and message, since silently
    swallowing a mismatched start/end callback pair would lose a span
    with no trace of the mistake — an unacceptable regression for the
    callback-fidelity guarantees Milestone 3's test strategy calls out.
    """


@dataclass
class _StructuralNode:
    """Lightweight stand-in for a `kind=None` "structural" node.

    Carries only what descendants need to keep the parent chain correct
    — `trace_id` to inherit, and `parent_span_id` already resolved to the
    nearest *exported* ancestor at the time this node was created (so
    resolution never needs to re-walk the chain later; each node just
    inherits its parent's already-resolved answer). It deliberately has
    no `kind`/`name`/`attributes`/etc. because it is never exported as a
    `Span` — see the module docstring and Architecture.md §3's
    fixed-4-kind decision for why such nodes must not be force-fit into
    a `SpanKind`.
    """

    trace_id: str
    span_id: str
    parent_span_id: Optional[str]


_TrackedEntry = Union[Span, _StructuralNode]


class SpanTracker:
    """Tracks in-flight spans for frameworks whose hooks fire
    start/end as two separate, unlinked callbacks.

    Keyed by an adapter-supplied ``external_id`` (whatever identifier the
    framework's own event carries — a LangGraph node-run id, an AutoGen
    message id, an OpenAI Agents SDK `Span.id`, ...), not by AgentMesh's
    own `span_id`, since the adapter only learns the AgentMesh identity
    *after* calling `start()`.
    """

    def __init__(self, tracer: Tracer) -> None:
        self.tracer = tracer
        self._tracked: Dict[str, _TrackedEntry] = {}

    def start(
        self,
        external_id: str,
        kind: SpanKind | None,
        name: str,
        *,
        parent_external_id: str | None = None,
        attributes: dict[str, str] | None = None,
    ) -> None:
        """Begin tracking a span for a framework start-callback.

        ``trace_id`` is inherited from the resolved parent (looked up via
        ``parent_external_id``), or freshly generated if there is no
        parent — mirroring `Tracer.start_span()`'s behavior for the
        context-manager case, minus the call-stack requirement.

        ``parent_span_id`` is the resolved parent's `span_id` when that
        parent was itself exported (its `kind` was not `None`); when the
        parent is structural, we reuse the parent's already-resolved
        nearest-exported-ancestor id, so a chain of any number of
        structural nodes collapses to a single lookup instead of walking
        upward at every level.

        ``kind=None`` marks a structural/no-op node: it is tracked (so
        later real descendants still resolve to the correct ancestor,
        and `finish()` can still be called on it symmetrically) but is
        never turned into an exported `Span` — see the module docstring
        and Architecture.md §3's fixed-4-kind decision.
        """
        parent = self._tracked.get(parent_external_id) if parent_external_id is not None else None

        if parent is not None:
            trace_id = parent.trace_id
            parent_span_id = parent.span_id if isinstance(parent, Span) else parent.parent_span_id
        else:
            trace_id = _new_trace_id()
            parent_span_id = None

        if kind is None:
            self._tracked[external_id] = _StructuralNode(
                trace_id=trace_id,
                span_id=_new_span_id(),
                parent_span_id=parent_span_id,
            )
            return

        span = Span(
            project_id=self.tracer.project_id,
            kind=kind,
            name=name,
            trace_id=trace_id,
            parent_span_id=parent_span_id,
        )
        if attributes:
            set_attrs(span, **attributes)
        self._tracked[external_id] = span

    def finish(
        self,
        external_id: str,
        *,
        status: SpanStatus = SpanStatus.OK,
        output: str | None = None,
        token_input: int | None = None,
        token_output: int | None = None,
        cost_usd: float | None = None,
    ) -> None:
        """Complete a tracked span for a framework end-callback.

        For a real (``kind is not None``) span, calls `Span.finish()` and
        hands it to the tracer's exporter directly
        (`self.tracer._exporter.record(...)`) — the same manual-export
        pattern used by `agentmesh_openai_agents.processor` today, needed
        because `Tracer.start_span()`'s context manager (which exports
        automatically) does not apply to event-based callback pairs.

        For a structural (``kind=None``) node, there is nothing to
        export; this is a no-op other than removing the tracking entry.

        Either way the entry is always removed from tracking. An unknown
        ``external_id`` raises `UnknownSpanError` rather than silently
        doing nothing — a mismatched start/end callback pair is a real
        bug an adapter needs to know about, not something to paper over.
        """
        try:
            entry = self._tracked.pop(external_id)
        except KeyError as exc:
            raise UnknownSpanError(
                f"SpanTracker.finish() called with unknown external_id={external_id!r}; "
                "was start() called for it, or was it already finished?"
            ) from exc

        if isinstance(entry, Span):
            entry.finish(
                status=status,
                output=output,
                token_input=token_input,
                token_output=token_output,
                cost_usd=cost_usd,
            )
            self.tracer._exporter.record(entry)
        # `_StructuralNode` entries carry nothing to export; popping the
        # tracking entry above is the entirety of "finishing" one.

    def active_trace_id(self, external_id: str) -> str | None:
        """Return the `trace_id` of a still-open tracked span, or `None`
        if ``external_id`` isn't currently tracked.

        Convenience for adapters that need to correlate a still-in-flight
        framework event with the AgentMesh trace it belongs to (e.g. to
        stamp a log line) without needing the full `Span`/`_StructuralNode`
        object.
        """
        entry = self._tracked.get(external_id)
        return entry.trace_id if entry is not None else None
