"""AgentMesh reference integration for the OpenAI Agents SDK (Milestone 3).

The OpenAI Agents SDK has its own native tracing system (`agents.tracing`):
`Runner.run(...)` opens a `Trace` and emits a tree of `Span[...]` objects
discriminated by `span.span_data.type` — `"task"` (the workflow root),
`"agent"` (an agent's turn), `"turn"` (one model round-trip), `"generation"`
(a model call), `"function"` (a tool invocation), and `"handoff"` (control
passed to another agent). This package's `AgentMeshTracingProcessor` is an
`agents.tracing.TracingProcessor` that mirrors the three adapter-usable
kinds onto an AgentMesh `agentmesh.Tracer` span, per Architecture.md §3's
fixed-3-kind mapping:

    | OpenAI Agents SDK `span_data.type` | -> | AgentMesh span kind |
    |-------------------------------------|----|----------------------|
    | `"generation"` (a model call)       | -> | `llm.call`           |
    | `"function"` (a tool invocation)    | -> | `tool.call`          |
    | `"handoff"` (control to another agent) | -> | `agent.handoff`   |

`"task"`/`"agent"`/`"turn"` are structural grouping spans with no
AgentMesh-kind equivalent; see `processor`'s module docstring for how
those are still tracked (for correct parent linkage) without being
exported, and for why `handoff` spans specifically start+finish together
in `on_span_end`.

This is the cleanest of the four framework mappings: the SDK's own
`handoff` span already carries `from_agent`/`to_agent`, a native 1:1
correspondence with `agent.handoff` requiring no re-derivation of intent
from a lower-level primitive (contrast LangGraph's edge-transition
inference or AutoGen's message-routing inference).

Usage::

    import agentmesh
    from agentmesh_openai_agents import AgentMeshTracingProcessor
    from agents.tracing import set_trace_processors

    tracer = agentmesh.configure(project_id=..., api_key=..., endpoint=...)
    set_trace_processors([AgentMeshTracingProcessor(tracer)])

Fake-model generation spans: a custom `Model` that doesn't go through the
SDK's built-in OpenAI-calling models (`openai_chatcompletions.py`,
`openai_responses.py`) will not automatically get wrapped in a
`generation_span` — those model implementations open the span themselves.
`FakeModel` in this package (used by the tests and the example app) opens
its own `agents.tracing.generation_span(...)` context manager around its
canned response, exactly mirroring what the SDK's real chat-completions
model class does, so the processor sees a `"generation"` span
(-> `llm.call`) for every simulated model call as well.

See `LIMITATIONS.md` for known gaps, including the SDK-version mismatch
this retrofit found and fixed (this package's original code targeted an
API shape that never shipped in any released `openai-agents` version).
"""

from ._fake_model import FakeModel, FakeModelProvider, ScriptedTurn
from .processor import AgentMeshTracingProcessor

__all__ = [
    "AgentMeshTracingProcessor",
    "FakeModel",
    "FakeModelProvider",
    "ScriptedTurn",
]

__version__ = "0.1.0"
