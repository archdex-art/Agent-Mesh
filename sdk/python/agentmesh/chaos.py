"""Chaos-engineering fault injection for AgentMesh-traced tool calls.

Real-world tool calls fail: a `web_search` API times out, a database read
returns a 500. Agents built and demoed only on the happy path have never
been exercised against those failures, so nobody knows whether the agent
recovers gracefully, retries sanely, or hallucinates a plausible-looking
but wrong answer. This module lets a developer deliberately inject latency
or errors into specific tool calls — the same mechanism site-reliability
engineers use for infrastructure chaos testing (Chaos Monkey, Gremlin),
applied to agent tool calls instead of network links.

Faults are opt-in and off by default (`ChaosPolicy.enabled = False`);
`configure_chaos()` must be called explicitly to turn chaos mode on, the
same way `agentmesh.configure()` must be called before tracing works at
all — a chaos-enabled SDK must never silently activate itself in a
production import.

When a fault fires, the wrapping span (built by tracer.py's
`_make_decorator`) is tagged with unprefixed `chaos.injected` /
`chaos.fault_type` attributes rather than `agentmesh.*`-prefixed ones —
docs/otlp-mapping.md's Collector-side contract silently drops any
unrecognized `agentmesh.*` key (a real bug caught and fixed in the MCP
Gateway's audit emitter), so any custom, non-core attribute rides the
same unprefixed "free-form metadata" passthrough path that contract
already documents.
"""

from __future__ import annotations

import random
import time
from dataclasses import dataclass, field
from typing import Union


class ChaosInjectedError(Exception):
    """Raised when a chaos-injected error fault fires in place of the
    wrapped tool call actually executing. A distinct exception type (rather
    than reusing TimeoutError/generic Exception) lets calling code and
    tests distinguish "this failure was deliberately injected" from a real
    bug, while `exception_type` on ErrorFault still lets a caller simulate
    a *specific* real exception type if that distinction matters for their
    agent's error-handling logic."""

    def __init__(self, message: str, fault_type: str) -> None:
        super().__init__(message)
        self.fault_type = fault_type


@dataclass(frozen=True)
class LatencyFault:
    """Injects artificial latency before the wrapped tool call executes,
    then lets the call proceed normally. Models a slow-but-eventually-
    successful upstream (e.g., a search API under load)."""

    seconds: float
    probability: float = 1.0  # 1.0 = deterministic; every matching call is delayed

    kind: str = field(default="latency", init=False)


@dataclass(frozen=True)
class ErrorFault:
    """Injects a simulated failure instead of executing the wrapped tool
    call. Models a hard upstream failure (timeout, 5xx, connection reset)."""

    message: str = "Simulated chaos failure"
    probability: float = 1.0  # 1.0 = deterministic; every matching call fails
    exception_type: type[Exception] = ChaosInjectedError

    kind: str = field(default="error", init=False)

    @staticmethod
    def timeout(message: str = "Simulated upstream timeout", probability: float = 1.0) -> "ErrorFault":
        """Convenience constructor modeling a network/upstream timeout."""
        return ErrorFault(message=message, probability=probability, exception_type=TimeoutError)

    @staticmethod
    def http_error(status_code: int = 500, probability: float = 1.0) -> "ErrorFault":
        """Convenience constructor modeling an upstream HTTP 5xx response.
        Raises ChaosInjectedError (not a framework-specific HTTP exception
        type, since the SDK has no dependency on any particular HTTP
        client) with the status code embedded in the message."""
        return ErrorFault(message=f"Simulated upstream HTTP {status_code}", probability=probability)


Fault = Union[LatencyFault, ErrorFault]


@dataclass
class ChaosPolicy:
    """A named set of faults, scoped per tool name, that a Tracer consults
    before executing a decorated tool/LLM call."""

    enabled: bool = False
    faults_by_tool: dict[str, list[Fault]] = field(default_factory=dict)
    _rng: random.Random = field(default_factory=random.Random)

    def faults_for(self, tool_name: str) -> list[Fault]:
        """Returns the configured faults for tool_name, or an empty list
        if chaos mode is disabled or no faults are configured for it."""
        if not self.enabled:
            return []
        return self.faults_by_tool.get(tool_name, [])

    def maybe_apply(self, tool_name: str) -> Fault | None:
        """Rolls each configured fault's probability in order and returns
        the first one that fires, or None if none fire (or none are
        configured). Only one fault fires per call — chaining multiple
        simultaneous faults onto a single call is not modeled, since a
        single failure mode is enough to exercise an agent's recovery
        logic and stacking faults would make failures harder to attribute."""
        for fault in self.faults_for(tool_name):
            if self._rng.random() < fault.probability:
                return fault
        return None


_default_policy = ChaosPolicy()


def configure_chaos(
    enabled: bool = True,
    faults_by_tool: dict[str, list[Fault]] | None = None,
    seed: int | None = None,
) -> ChaosPolicy:
    """Initialize the module-level default ChaosPolicy consulted by every
    @trace_llm_call / @trace_tool_call decorated function. `seed` makes
    fault selection reproducible for tests/demos; omit it for real
    randomized chaos testing."""
    global _default_policy
    rng = random.Random(seed) if seed is not None else random.Random()
    _default_policy = ChaosPolicy(enabled=enabled, faults_by_tool=faults_by_tool or {}, _rng=rng)
    return _default_policy


def get_chaos_policy() -> ChaosPolicy:
    """Returns the currently active ChaosPolicy (module-level default
    unless configure_chaos() was called)."""
    return _default_policy


def apply_fault(fault: Fault) -> None:
    """Executes a fault's effect at the call site: sleeps for a
    LatencyFault (call proceeds afterward), or raises for an ErrorFault
    (call never executes). Separated from ChaosPolicy.maybe_apply so the
    decision (which fault, if any) and the effect (what actually happens)
    are independently testable."""
    if isinstance(fault, LatencyFault):
        time.sleep(fault.seconds)
    elif isinstance(fault, ErrorFault):
        if fault.exception_type is ChaosInjectedError:
            raise ChaosInjectedError(fault.message, fault.kind)
        raise fault.exception_type(fault.message)
