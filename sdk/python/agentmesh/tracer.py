"""AgentMesh's public instrumentation API: the manual wrapping decorators
specified in Milestones.md's Milestone 2 deliverable ("manual wrapping API
(@agentmesh.trace_llm_call, @agentmesh.trace_tool_call decorators)").

This is the fallback instrumentation path for custom agent loops; the
framework-specific reference integrations built in Milestone 3
(sdk/integrations/{langgraph,crewai,autogen,openai-agents-sdk}) call the
same underlying Tracer.start_span API these decorators use, so both paths
produce identical spans.
"""

from __future__ import annotations

import functools
import json
from contextlib import contextmanager
from typing import Any, Callable, Iterator, TypeVar

from . import chaos, replay_shim
from ._span import Span, SpanKind, SpanStatus, _new_trace_id
from .exporter import BatchingExporter

F = TypeVar("F", bound=Callable[..., Any])


class Tracer:
    """Owns the exporter and the current-span stack (for parent/child
    linking across nested decorated calls within one thread)."""

    def __init__(self, project_id: str, exporter: BatchingExporter) -> None:
        self.project_id = project_id
        self._exporter = exporter
        self._span_stack: list[Span] = []

    @contextmanager
    def start_span(self, kind: SpanKind, name: str) -> Iterator[Span]:
        """Start a span, linking it to the currently-active span (if any)
        as its parent — this is what lets nested decorated calls produce a
        correctly-shaped span tree without the caller manually threading
        parent IDs through."""
        parent = self._span_stack[-1] if self._span_stack else None
        span = Span(
            project_id=self.project_id,
            kind=kind,
            name=name,
            parent_span_id=parent.span_id if parent else None,
            trace_id=parent.trace_id if parent else _new_trace_id(),
        )
        replay_id = replay_shim.current_replay_id()
        if replay_id is not None:
            # Tags every span produced during an execution-mode replay run
            # with the source AGENTMESH_REPLAY_ID (unprefixed `replay.*`,
            # matching chaos.py's rationale for non-`agentmesh.*` custom
            # attributes), so the Replay Engine can find this replay's
            # newly-generated trace_id in ClickHouse — the replaying
            # process gets a fresh trace_id (this is a *new* execution,
            # not a mutation of the original), and this attribute is the
            # only link back to which replay run produced it.
            span.attributes["replay.replay_id"] = replay_id
        self._span_stack.append(span)
        try:
            yield span
        except Exception:
            if span.status is None:  # caller's wrapped function raised before calling finish()
                span.finish(status=SpanStatus.ERROR)
            raise
        finally:
            self._span_stack.pop()
            if span.status is None:
                # The wrapped call returned normally but never called
                # span.finish() itself (the common case for the decorator
                # API below, which finishes the span automatically) — this
                # branch only fires for callers using start_span directly
                # without finishing it, which we still export rather than
                # silently drop, defaulting to OK.
                span.finish(status=SpanStatus.OK)
            self._exporter.record(span)

    def shutdown(self) -> None:
        self._exporter.shutdown()


_default_tracer: Tracer | None = None


def configure(
    project_id: str,
    api_key: str,
    endpoint: str = "localhost:4317",
    **exporter_kwargs: Any,
) -> Tracer:
    """Initialize the module-level default Tracer used by the
    @trace_llm_call / @trace_tool_call decorators. Must be called once
    before those decorators are used; calling it again replaces the
    previous tracer (e.g., to reconfigure in tests)."""
    global _default_tracer
    exporter = BatchingExporter(endpoint, api_key, **exporter_kwargs)
    _default_tracer = Tracer(project_id, exporter)
    return _default_tracer


def _require_tracer() -> Tracer:
    if _default_tracer is None:
        raise RuntimeError(
            "agentmesh: configure() must be called before using @trace_llm_call "
            "or @trace_tool_call (no active tracer)"
        )
    return _default_tracer


def _json_or_str(value: Any) -> str:
    """Best-effort serialization of a wrapped function's args/return value
    for span input/output — never raises on a non-JSON-serializable value,
    since a serialization failure must never crash the customer's agent
    (Architecture.md §17's ingestion-path availability philosophy applies
    here too)."""
    try:
        return json.dumps(value, default=str)
    except (TypeError, ValueError):
        return str(value)


def trace_llm_call(name: str | None = None) -> Callable[[F], F]:
    """Decorator marking a function as an LLM call span
    (shared/span.KindLLMCall). `name` defaults to the wrapped function's
    `__name__` if not given."""
    return _make_decorator(SpanKind.LLM_CALL, name)


def trace_tool_call(name: str | None = None) -> Callable[[F], F]:
    """Decorator marking a function as a tool call span
    (shared/span.KindToolCall)."""
    return _make_decorator(SpanKind.TOOL_CALL, name)


def _make_decorator(kind: SpanKind, explicit_name: str | None) -> Callable[[F], F]:
    def decorator(func: F) -> F:
        span_name = explicit_name or func.__name__

        @functools.wraps(func)
        def wrapper(*args: Any, **kwargs: Any) -> Any:
            tracer = _require_tracer()
            with tracer.start_span(kind, span_name) as span:
                if args or kwargs:
                    span.set_input(_json_or_str({"args": args, "kwargs": kwargs}))

                # Time-Travel Replay hook: takes priority over chaos and
                # the real call. When AGENTMESH_REPLAY_ID is set, this
                # process is reconstructing a historical trace
                # (Architecture.md §7) — the wrapped function must never
                # actually execute, since execution-mode replay's entire
                # value proposition is re-running current code against
                # *recorded* data with zero real side effects (no
                # double-charging a payment API, no duplicate emails).
                if replay_shim.is_active():
                    call_index = replay_shim.next_call_index(kind.value, span_name)
                    recorded = replay_shim.fetch_recorded_response(kind.value, span_name, call_index)
                    span.attributes["replay.replayed"] = "true"
                    span.attributes["replay.call_index"] = str(call_index)
                    if recorded.status != SpanStatus.OK:
                        span.finish(status=recorded.status, output=recorded.output)
                        raise replay_shim.ReplayedCallError(recorded.status, recorded.output)
                    span.finish(status=SpanStatus.OK, output=recorded.output)
                    return recorded.output

                # Chaos Engineering hook: before the wrapped call executes,
                # consult the active ChaosPolicy for this tool/LLM name. A
                # fired fault is tagged onto the span (unprefixed
                # `chaos.*` attributes — see chaos.py's module docstring
                # for why not `agentmesh.chaos.*`) so the trace explicitly
                # distinguishes an injected failure from a real one; a
                # LatencyFault delays execution but still calls through,
                # while an ErrorFault raises before func() ever runs.
                policy = chaos.get_chaos_policy()
                fault = policy.maybe_apply(span_name)
                if fault is not None:
                    span.attributes["chaos.injected"] = "true"
                    span.attributes["chaos.fault_type"] = fault.kind
                    try:
                        chaos.apply_fault(fault)
                    except Exception as exc:
                        span.finish(status=SpanStatus.ERROR, output=_json_or_str({"error": str(exc)}))
                        raise
                    # LatencyFault falls through to execute func() below,
                    # having already slept.

                result = func(*args, **kwargs)
                span.finish(status=SpanStatus.OK, output=_json_or_str(result))
                return result

        return wrapper  # type: ignore[return-value]

    return decorator
