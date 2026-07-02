"""Generic "framework enum -> SpanKind or None" translation helper.

Every event-based adapter needs to answer the same question for each
framework-native event it observes: "does this correspond to an
`llm.call`, a `tool.call`, an `agent.handoff`, or is it structural
scaffolding that AgentMesh should not export as its own span (`None`)?"
`map_or_none` is the one-line implementation of that lookup so adapters
don't each hand-roll their own if/elif ladder.

The canonical mapping every adapter in this repo should follow is the
table from `docs/plan/Architecture.md` §3 ("Agent Architecture"),
reproduced here verbatim as the single source of truth adapters should
consult when deciding their own framework-specific mapping dict. This
comment is documentation only — it is not enforced in code, since each
framework's native concepts are typed differently and each adapter owns
its own mapping dict passed into `map_or_none`.

    | Framework          | -> llm.call              | -> tool.call                 | -> agent.handoff                                   |
    |--------------------|---------------------------|-------------------------------|-----------------------------------------------------|
    | LangGraph          | node LLM invocation       | node tool invocation          | edge transition between graph nodes (different agents) |
    | CrewAI             | agent's LLM call          | task tool execution           | task delegation between crew members                |
    | AutoGen            | agent message generation  | function/tool call in message | message routed to a different agent in the group chat |
    | OpenAI Agents SDK  | `Runner` LLM step         | `FunctionTool` invocation      | `handoff` primitive (native 1:1 mapping)            |
    | Custom loop        | wrapped LLM client call   | wrapped tool dispatcher call   | wrapped sub-agent invocation                        |

    Anything a framework produces that isn't one of the three columns above
    (e.g. LangGraph's plain reducer/routing nodes, CrewAI's crew-level
    lifecycle events, AutoGen's group-chat-manager bookkeeping messages,
    OpenAI Agents SDK's `agent_span`/`task_span` grouping spans) maps to
    `None` — meaning "structural, track for parent-chain purposes only,
    never export" (see `context.SpanTracker`).
"""

from __future__ import annotations

from typing import Any, Hashable, Mapping, Optional, TypeVar

_V = TypeVar("_V")

__all__ = ["map_or_none"]


def map_or_none(
    value: Hashable, mapping: Mapping[Any, _V], default: Optional[_V] = None
) -> Optional[_V]:
    """Look up ``value`` in ``mapping``, returning ``default`` if absent.

    Thin wrapper around `dict.get`, but named and documented so adapter
    code reads as an explicit "translate this framework-native event kind
    into a `SpanKind` (or `None` for structural events)" step rather than
    an anonymous dict lookup. ``default`` is `None` by design: an unknown
    or structural framework event should default to "don't export a span
    for this," never silently coerce to some arbitrary `SpanKind`.
    """
    return mapping.get(value, default)
