"""agentmesh_integrations_common: shared building blocks for AgentMesh's
framework-agnostic reference integrations (Milestone 3).

Every adapter package (LangGraph, CrewAI, AutoGen, OpenAI Agents SDK, and
any future one) is expected to depend on this package rather than
re-implementing:

- `spans` — the fixed-4-kind span model conventions (`SpanKind`,
  `SpanStatus`, `FRAMEWORK_ATTR`, `set_attrs`).
- `mapper` — `map_or_none`, the "framework enum -> SpanKind or None"
  translation helper, plus the canonical Architecture.md §3 mapping
  table as reference documentation.
- `context` — `SpanTracker`, for frameworks whose start/end callbacks
  fire asynchronously rather than nesting as context managers.
- `adapter` — `FrameworkAdapter`, the shared constructor shape.
- `testing` — `FakeExporter` and golden-trace assertions for adapter
  test suites.

See each submodule's docstring for the design rationale.
"""

from .adapter import FrameworkAdapter
from .context import SpanTracker, UnknownSpanError
from .mapper import map_or_none
from .spans import FRAMEWORK_ATTR, SpanKind, SpanStatus, set_attrs

__all__ = [
    "FRAMEWORK_ATTR",
    "FrameworkAdapter",
    "SpanKind",
    "SpanStatus",
    "SpanTracker",
    "UnknownSpanError",
    "map_or_none",
    "set_attrs",
]
