"""agentmesh: instrumentation SDK for tracing AI agents.

Quick start::

    import agentmesh

    agentmesh.configure(project_id="...", api_key="am_live_...")

    @agentmesh.trace_llm_call()
    def call_model(prompt: str) -> str:
        ...

    @agentmesh.trace_tool_call()
    def search(query: str) -> str:
        ...

Chaos engineering (resilience testing)::

    from agentmesh.chaos import ErrorFault, LatencyFault

    agentmesh.configure_chaos(faults_by_tool={
        "search": [ErrorFault.timeout(probability=0.2)],
    })

See docs/otlp-mapping.md for the underlying wire contract and
Architecture.md for how this SDK fits into the rest of AgentMesh.
"""

from ._span import CURRENT_SCHEMA_VERSION, Span, SpanKind, SpanStatus
from .chaos import (
    ChaosInjectedError,
    ChaosPolicy,
    ErrorFault,
    LatencyFault,
    configure_chaos,
    get_chaos_policy,
)
from .replay_shim import ReplayedCallError, ReplayLookupError
from .tracer import Tracer, configure, trace_llm_call, trace_tool_call

__all__ = [
    "CURRENT_SCHEMA_VERSION",
    "ChaosInjectedError",
    "ChaosPolicy",
    "ErrorFault",
    "LatencyFault",
    "ReplayLookupError",
    "ReplayedCallError",
    "Span",
    "SpanKind",
    "SpanStatus",
    "Tracer",
    "configure",
    "configure_chaos",
    "get_chaos_policy",
    "trace_llm_call",
    "trace_tool_call",
]

__version__ = "0.1.0"
