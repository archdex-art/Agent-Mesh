"""AgentMesh reference integration for LangGraph (Milestone 3).

LangGraph has no native "this is an LLM call" / "this is a tool call" /
"this is a handoff to another agent" concept the way CrewAI (`Agent`/
`Task`) or the OpenAI Agents SDK (typed `Span` subclasses) do — a
LangGraph node is just an arbitrary Python callable (or `Runnable`) that
reads and returns graph state. So instead of guessing kind from node
*shape*, this adapter uses the one first-class extension point LangGraph
gives node authors for exactly this purpose: `StateGraph.add_node(...,
metadata={...})`. That metadata dict is threaded, unmodified, onto every
callback event LangGraph fires for that node's execution (verified
against the installed `langgraph`/`langchain-core` versions — see
`LIMITATIONS.md`), so a graph author tags each node once, at graph-build
time, with the canonical kind that node's execution should map to:

    from agentmesh_langgraph import node_kind_metadata
    from agentmesh_integrations_common import SpanKind

    graph.add_node("search", search_fn, metadata=node_kind_metadata(SpanKind.TOOL_CALL))
    graph.add_node("review", review_fn, metadata=node_kind_metadata(SpanKind.AGENT_HANDOFF))
    graph.add_node("route", route_fn)  # untagged -> structural, kind=None

A node with no `agentmesh_kind` metadata entry is treated as structural
bookkeeping (`kind=None`, per `context.SpanTracker`) — the right default
for LangGraph's own routing/conditional-edge nodes, which have no
workflow-visible action of their own.

Wiring mechanism
-----------------
LangGraph (built on `langchain-core`'s `Runnable`/callback machinery)
fires a matched `on_chain_start`/`on_chain_end` (or `on_chain_error`) pair
through any `BaseCallbackHandler` registered in a run's `RunnableConfig`
for *every* graph-level event: one pair for the overall
`.invoke()`/`.stream()` call (the "graph root", `parent_run_id is None`)
and one pair per top-level node execution (`parent_run_id` is the graph
root's `run_id`, `metadata["langgraph_node"]` is the node's name).
`instrument()` installs a `_TracingCallbackHandler` by wrapping
`invoke`/`ainvoke`/`stream`/`astream` on the target so every future call
carries it automatically — callers never pass `config={"callbacks": [...]}`
themselves.

As a bonus (not required by the shared workflow, but real LangGraph
agents built from LangChain-native primitives benefit from it for free):
`on_llm_start`/`on_chat_model_start` and `on_tool_start` fire whenever a
node's implementation invokes a real LangChain `Runnable` chat model or
`@tool`, regardless of nesting depth, and are mapped straight onto
`LLM_CALL`/`TOOL_CALL` using their own true `parent_run_id` — no
`add_node(metadata=...)` tagging required for that path.

See `LIMITATIONS.md` for the specific fidelity gaps this design accepts.
"""

from __future__ import annotations

import functools
import json
from typing import Any, Callable, Optional
from uuid import UUID

from agentmesh_integrations_common import (
    FRAMEWORK_ATTR,
    FrameworkAdapter,
    SpanKind,
    SpanStatus,
    map_or_none,
)
from langchain_core.callbacks import BaseCallbackHandler
from langgraph.graph import StateGraph

__all__ = ["LangGraphAdapter", "node_kind_metadata", "NODE_KIND_METADATA_KEY"]

# The `add_node(..., metadata={...})` key a graph author sets to declare
# which AgentMesh `SpanKind` a node's execution corresponds to. Values are
# the `SpanKind` *string* values ("llm.call"/"tool.call"/"agent.handoff")
# so the metadata dict stays plain-JSON-serializable (LangGraph forwards
# it into LangSmith/tracing metadata elsewhere too) rather than carrying a
# live `SpanKind` enum member.
NODE_KIND_METADATA_KEY = "agentmesh_kind"

_KIND_BY_LABEL: dict[str, SpanKind] = {
    SpanKind.LLM_CALL.value: SpanKind.LLM_CALL,
    SpanKind.TOOL_CALL.value: SpanKind.TOOL_CALL,
    SpanKind.AGENT_HANDOFF.value: SpanKind.AGENT_HANDOFF,
}


def node_kind_metadata(kind: "SpanKind | str | None" = None, **extra: Any) -> dict[str, Any]:
    """Build the `add_node(..., metadata=...)` dict `LangGraphAdapter` reads
    to decide a node's exported span kind.

    ``kind`` is one of `SpanKind.LLM_CALL`/`SpanKind.TOOL_CALL`/
    `SpanKind.AGENT_HANDOFF` (or the matching string). Omit it (or pass
    `None`) for a structural/routing node with no `agentmesh.SpanKind`
    equivalent — the resulting dict then carries none of the reserved
    kind key, which is exactly what makes `LangGraphAdapter` treat that
    node as structural. ``**extra`` passes through any other metadata the
    graph author wants attached to the node (LangGraph forwards all of it
    to tracers/callbacks alongside the reserved key).
    """
    metadata = dict(extra)
    if kind is not None:
        metadata[NODE_KIND_METADATA_KEY] = kind.value if isinstance(kind, SpanKind) else str(kind)
    return metadata


def _stringify(value: Any) -> str:
    try:
        return json.dumps(value, default=str)
    except TypeError:
        return str(value)


def _model_name(serialized: Optional[dict], kwargs: dict) -> str:
    name = kwargs.get("name")
    if name:
        return str(name)
    if serialized:
        candidate = serialized.get("name") or (serialized.get("id") or [None])[-1]
        if candidate:
            return str(candidate)
    return "llm"


def _tool_name(serialized: Optional[dict], kwargs: dict) -> str:
    name = kwargs.get("name")
    if name:
        return str(name)
    if serialized:
        candidate = serialized.get("name")
        if candidate:
            return str(candidate)
    return "tool"


def _merge_callbacks(config: Optional[dict], handler: BaseCallbackHandler) -> dict:
    """Return a copy of ``config`` with ``handler`` appended to its
    `callbacks` list, preserving whatever else the caller set.

    Identity-checked against whatever's already there: `CompiledStateGraph`
    methods delegate to one another internally (empirically, `.invoke()`
    calls `.stream()` under the hood, `.ainvoke()`/`.astream()` likely the
    same), and since we wrap all four independently, an uninstrumented
    inner call would otherwise re-merge the *same* handler instance a
    second time onto the config the outer wrapper already built — which
    LangChain's callback manager would then invoke twice per event. Since
    the inner call receives the outer call's already-merged config object,
    a simple "is this exact handler already in the list" check is enough
    to make wrapping every entry point idempotent regardless of which of
    them happens to delegate to which internally.
    """
    merged = dict(config) if config else {}
    existing = merged.get("callbacks")
    if existing is None:
        callbacks = [handler]
    else:
        try:
            if any(cb is handler for cb in existing):
                return merged
            callbacks = [*existing, handler]
        except TypeError:
            # A `BaseCallbackManager` instance rather than a plain list —
            # best-effort: still get our handler into the run.
            callbacks = [existing, handler]
    merged["callbacks"] = callbacks
    return merged


class _TracingCallbackHandler(BaseCallbackHandler):
    """Translates `langchain-core` callback events into `SpanTracker`
    start/finish calls. Kept as a thin dispatcher; all decision logic
    (kind resolution, parent-chain bookkeeping) lives on the adapter so
    one adapter instance can share it across every graph it instruments.
    """

    def __init__(self, adapter: "LangGraphAdapter") -> None:
        self._adapter = adapter

    # -- per-node / graph-invocation chain events -----------------------
    def on_chain_start(
        self,
        serialized,
        inputs,
        *,
        run_id: UUID,
        parent_run_id: Optional[UUID] = None,
        tags=None,
        metadata=None,
        **kwargs: Any,
    ) -> None:
        self._adapter._on_chain_start(run_id, parent_run_id, metadata or {}, kwargs)

    def on_chain_end(self, outputs, *, run_id: UUID, parent_run_id: Optional[UUID] = None, **kwargs: Any) -> None:
        self._adapter._on_chain_end(run_id, outputs)

    def on_chain_error(self, error, *, run_id: UUID, parent_run_id: Optional[UUID] = None, **kwargs: Any) -> None:
        self._adapter._on_chain_error(run_id, error)

    # -- native LangChain model sub-calls (bonus fidelity) ---------------
    def on_llm_start(self, serialized, prompts, *, run_id: UUID, parent_run_id: Optional[UUID] = None, tags=None, metadata=None, **kwargs: Any) -> None:
        self._adapter._on_model_start(run_id, parent_run_id, serialized, kwargs)

    def on_chat_model_start(self, serialized, messages, *, run_id: UUID, parent_run_id: Optional[UUID] = None, tags=None, metadata=None, **kwargs: Any) -> None:
        self._adapter._on_model_start(run_id, parent_run_id, serialized, kwargs)

    def on_llm_end(self, response, *, run_id: UUID, parent_run_id: Optional[UUID] = None, **kwargs: Any) -> None:
        self._adapter._on_model_end(run_id, response)

    def on_llm_error(self, error, *, run_id: UUID, parent_run_id: Optional[UUID] = None, **kwargs: Any) -> None:
        self._adapter._on_model_error(run_id, error)

    # -- native LangChain tool sub-calls (bonus fidelity) ----------------
    def on_tool_start(self, serialized, input_str, *, run_id: UUID, parent_run_id: Optional[UUID] = None, tags=None, metadata=None, inputs=None, **kwargs: Any) -> None:
        self._adapter._on_tool_start(run_id, parent_run_id, serialized, input_str, kwargs)

    def on_tool_end(self, output, *, run_id: UUID, parent_run_id: Optional[UUID] = None, **kwargs: Any) -> None:
        self._adapter._on_tool_end(run_id, output)

    def on_tool_error(self, error, *, run_id: UUID, parent_run_id: Optional[UUID] = None, **kwargs: Any) -> None:
        self._adapter._on_tool_error(run_id, error)


class LangGraphAdapter(FrameworkAdapter):
    """Instruments a LangGraph `StateGraph`/compiled graph to emit
    AgentMesh spans for the shared Milestone 3 workflow (and, more
    generally, any LangGraph graph whose nodes are tagged via
    `node_kind_metadata()`).

    Usage::

        adapter = LangGraphAdapter(tracer)
        adapter.instrument(compiled_graph)
        compiled_graph.invoke(initial_state)  # spans emitted automatically
    """

    def __init__(self, tracer) -> None:
        super().__init__(tracer)
        # root run_id -> external_id of whichever node most recently
        # transitioned into this graph invocation ("chain tip"): the
        # linear parent chain the assignment calls for, distinct from
        # LangGraph's own Pregel nesting (every top-level node's native
        # `parent_run_id` is the *graph root*, not the previous node,
        # since same-superstep nodes can in general run in parallel —
        # see LIMITATIONS.md).
        self._chain_tip: dict[str, str] = {}
        # top-level node run_id -> its root run_id; also doubles as
        # "is this run_id a top-level node" for `_on_chain_end`/`_on_chain_error`,
        # which get no `metadata` to check `langgraph_node` against.
        self._node_root: dict[str, str] = {}
        # top-level node run_id -> (stringified output, status) for a node
        # whose native `on_chain_end`/`on_chain_error` already fired but
        # whose `SpanTracker.finish()` call is deliberately delayed — see
        # `_flush_pending`'s docstring for why.
        self._pending: dict[str, tuple[str, SpanStatus]] = {}

    # -- public API -------------------------------------------------
    def instrument(self, target: Any) -> None:
        """Wire tracing onto ``target``.

        ``target`` may be an uncompiled `StateGraph` (its `.compile()` is
        wrapped so the graph it produces is instrumented automatically)
        or an already-compiled graph (its `invoke`/`ainvoke`/`stream`/
        `astream` are wrapped directly).
        """
        if isinstance(target, StateGraph):
            self._instrument_state_graph(target)
        else:
            self._instrument_compiled_graph(target)

    # -- wiring -------------------------------------------------------
    def _instrument_state_graph(self, state_graph: StateGraph) -> None:
        original_compile = state_graph.compile
        adapter = self

        @functools.wraps(original_compile)
        def wrapped_compile(*args: Any, **kwargs: Any) -> Any:
            compiled = original_compile(*args, **kwargs)
            adapter._instrument_compiled_graph(compiled)
            return compiled

        state_graph.compile = wrapped_compile

    def _instrument_compiled_graph(self, compiled: Any) -> None:
        handler = _TracingCallbackHandler(self)
        for method_name in ("invoke", "stream", "ainvoke", "astream"):
            original = getattr(compiled, method_name, None)
            if original is None:
                continue
            setattr(compiled, method_name, self._wrap_run_method(original, handler))

    @staticmethod
    def _wrap_run_method(original: Callable[..., Any], handler: BaseCallbackHandler) -> Callable[..., Any]:
        # One wrapper shape covers sync (`invoke`), sync-generator
        # (`stream`), coroutine (`ainvoke`), and async-generator
        # (`astream`) methods alike: in every case, *calling* the
        # original returns the right kind of object (a value, an
        # iterator, a coroutine, or an async iterator) without needing
        # `await`/`yield from` here — only *consuming* those objects
        # would require that, and callers do that themselves.
        @functools.wraps(original)
        def wrapped(input: Any = None, config: Optional[dict] = None, *args: Any, **kwargs: Any) -> Any:
            return original(input, _merge_callbacks(config, handler), *args, **kwargs)

        return wrapped

    # -- callback event handling ---------------------------------------
    def _on_chain_start(self, run_id: UUID, parent_run_id: Optional[UUID], metadata: dict, kwargs: dict) -> None:
        run_id = str(run_id)
        parent_run_id = str(parent_run_id) if parent_run_id is not None else None
        node_name = metadata.get("langgraph_node")

        if parent_run_id is None:
            # The overall `.invoke()`/`.stream()` call: structural root
            # anchoring the whole trace's parent chain.
            root_name = kwargs.get("name") or "langgraph.graph"
            self.spans.start(external_id=run_id, kind=None, name=root_name)
            self._chain_tip[run_id] = run_id
            return

        if node_name is not None and parent_run_id in self._chain_tip:
            # A genuine top-level graph-node execution: LangGraph nests
            # every same-superstep node directly under the graph root, so
            # `parent_run_id` alone can't express "ran after node X" —
            # `_chain_tip` supplies that ordering instead. Crucially, the
            # referenced tip is still *open* in `SpanTracker` at this
            # point (see `_flush_pending`) even though its own native
            # `on_chain_end` already fired, which is what makes a real
            # parent/child link (not just a shared trace_id) possible.
            root_id = parent_run_id
            current_tip = self._chain_tip[root_id]
            kind_label = metadata.get(NODE_KIND_METADATA_KEY)
            kind = map_or_none(kind_label, _KIND_BY_LABEL, default=None)
            attributes = (
                {FRAMEWORK_ATTR: "langgraph", "langgraph.node": node_name}
                if kind is not None
                else None
            )
            self.spans.start(
                external_id=run_id,
                kind=kind,
                name=node_name,
                parent_external_id=current_tip,
                attributes=attributes,
            )
            # Now that `current_tip` has served as this node's parent,
            # its own deferred finish (if any — nothing is pending the
            # very first time, when `current_tip` is the root itself)
            # can safely fire.
            self._flush_pending(current_tip)
            self._chain_tip[root_id] = run_id
            self._node_root[run_id] = root_id
            return

        # Anything else reaching `on_chain_start` (LangGraph's internal
        # branch-executor/RunnableSeq wrapping around a node that has
        # `add_conditional_edges` attached, retry-policy re-invocation
        # plumbing, etc.) is structural bookkeeping only: `metadata` here
        # is *inherited* from the enclosing node, not a fresh declaration
        # for this specific internal run, so honoring
        # `NODE_KIND_METADATA_KEY` again would double-export the node.
        self.spans.start(
            external_id=run_id,
            kind=None,
            name=node_name or kwargs.get("name") or "langgraph.internal",
            parent_external_id=parent_run_id,
        )

    def _flush_pending(self, external_id: str) -> None:
        """Finish a top-level node's `SpanTracker` entry once it is no
        longer needed as an *open* parent reference.

        `_on_chain_end`/`_on_chain_error` fire for a top-level node
        *before* the next node (or the graph root) is known to need it as
        a parent — but `SpanTracker.finish()` immediately pops its entry,
        and a `parent_external_id` that no longer resolves starts a
        *new* trace (see `SpanTracker.start()`), which would silently
        fragment one graph run into several trace ids. So a top-level
        node's actual `SpanTracker.finish()` call is deferred here, to
        the moment right after whichever span needs it as a parent has
        already used it — either the next chained node (`_on_chain_start`)
        or, for the very last node, the graph root itself
        (`_on_chain_end`/`_on_chain_error`, root case). A no-op for
        anything not currently pending (e.g. `current_tip` being the
        root itself, which is never stashed here).
        """
        pending = self._pending.pop(external_id, None)
        if pending is not None:
            output, status = pending
            self.spans.finish(external_id=external_id, status=status, output=output)
        self._node_root.pop(external_id, None)

    def _on_chain_end(self, run_id: UUID, outputs: Any) -> None:
        run_id = str(run_id)
        if run_id in self._chain_tip:
            # The graph root: flush whichever node last ran (there is no
            # further node to do it via `_on_chain_start`), then finish
            # the root itself.
            self._flush_pending(self._chain_tip.pop(run_id))
            self.spans.finish(external_id=run_id, status=SpanStatus.OK, output=_stringify(outputs))
            return
        if run_id in self._node_root:
            # A top-level node: stash for `_flush_pending` rather than
            # finishing immediately — see its docstring.
            self._pending[run_id] = (_stringify(outputs), SpanStatus.OK)
            return
        # Internal/nested structural event: nothing else depends on
        # keeping it open, so finish immediately.
        self.spans.finish(external_id=run_id, status=SpanStatus.OK, output=_stringify(outputs))

    def _on_chain_error(self, run_id: UUID, error: BaseException) -> None:
        run_id = str(run_id)
        if run_id in self._chain_tip:
            self._flush_pending(self._chain_tip.pop(run_id))
            self.spans.finish(external_id=run_id, status=SpanStatus.ERROR, output=str(error))
            return
        if run_id in self._node_root:
            self._pending[run_id] = (str(error), SpanStatus.ERROR)
            return
        self.spans.finish(external_id=run_id, status=SpanStatus.ERROR, output=str(error))

    def _on_model_start(self, run_id: UUID, parent_run_id: Optional[UUID], serialized: Optional[dict], kwargs: dict) -> None:
        run_id = str(run_id)
        parent_run_id = str(parent_run_id) if parent_run_id is not None else None
        name = _model_name(serialized, kwargs)
        self.spans.start(
            external_id=run_id,
            kind=SpanKind.LLM_CALL,
            name=name,
            parent_external_id=parent_run_id,
            attributes={FRAMEWORK_ATTR: "langgraph", "langgraph.model": name},
        )

    def _on_model_end(self, run_id: UUID, response: Any) -> None:
        self.spans.finish(external_id=str(run_id), status=SpanStatus.OK, output=_stringify(response))

    def _on_model_error(self, run_id: UUID, error: BaseException) -> None:
        self.spans.finish(external_id=str(run_id), status=SpanStatus.ERROR, output=str(error))

    def _on_tool_start(self, run_id: UUID, parent_run_id: Optional[UUID], serialized: Optional[dict], input_str: str, kwargs: dict) -> None:
        run_id = str(run_id)
        parent_run_id = str(parent_run_id) if parent_run_id is not None else None
        name = _tool_name(serialized, kwargs)
        self.spans.start(
            external_id=run_id,
            kind=SpanKind.TOOL_CALL,
            name=name,
            parent_external_id=parent_run_id,
            attributes={FRAMEWORK_ATTR: "langgraph", "langgraph.tool": name},
        )

    def _on_tool_end(self, run_id: UUID, output: Any) -> None:
        self.spans.finish(external_id=str(run_id), status=SpanStatus.OK, output=_stringify(output))

    def _on_tool_error(self, run_id: UUID, error: BaseException) -> None:
        self.spans.finish(external_id=str(run_id), status=SpanStatus.ERROR, output=str(error))
