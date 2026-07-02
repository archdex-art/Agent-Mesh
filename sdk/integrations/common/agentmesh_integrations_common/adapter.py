"""Shared base class for AgentMesh framework instrumentation adapters.

Every Milestone 3 reference integration (LangGraph, CrewAI, AutoGen,
OpenAI Agents SDK) needs the same two things to start: a handle on the
`agentmesh.Tracer` it exports to, and a `context.SpanTracker` for
threading parent/child links through that framework's callback style.
`FrameworkAdapter` exists purely to give every adapter package one
shared constructor shape instead of each redeclaring
`self.tracer = tracer; self.spans = SpanTracker(tracer)` (or, worse,
reinventing its own ad hoc dict-based span-tracking scheme, as
`agentmesh_openai_agents.processor` did before `context.SpanTracker`
existed to share it).

This is deliberately the entire abstraction: no plugin registry, no
dynamic hook-dispatch machinery, no lifecycle beyond "construct, then
call `instrument()`". Each concrete adapter's `instrument()` method wires
up whatever hooks its framework exposes (LangGraph node callbacks,
AutoGen message hooks, a `TracingProcessor`, ...) using `self.spans`
directly.
"""

from __future__ import annotations

import abc
from typing import Any

from agentmesh import Tracer

from .context import SpanTracker

__all__ = ["FrameworkAdapter"]


class FrameworkAdapter(abc.ABC):
    """Common constructor + `SpanTracker` shared by every adapter package."""

    def __init__(self, tracer: Tracer) -> None:
        self.tracer = tracer
        self.spans = SpanTracker(tracer)

    @abc.abstractmethod
    def instrument(self, target: Any) -> None:
        """Wire this adapter's framework-specific hooks/callbacks onto
        ``target`` (the framework object being instrumented — a graph,
        a crew, a group chat, a runner, etc.), using `self.spans` to
        emit `llm.call`/`tool.call`/`agent.handoff` spans as the
        framework's own events fire.
        """
        raise NotImplementedError
