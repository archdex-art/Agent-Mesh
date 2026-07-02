"""Deterministic, side-effect-free "tools" shared by every Milestone 3
example app (LangGraph, CrewAI, AutoGen, OpenAI Agents SDK).

Each framework wires these plain functions into its own tool-calling
mechanism (a CrewAI `@tool`, a LangGraph node, an AutoGen function tool, an
OpenAI Agents SDK `function_tool`, ...). They deliberately do:

- No network calls.
- No randomness, no wall-clock-dependent output.
- No dependency on any framework or on the `agentmesh` SDK.

so that every example app produces byte-identical tool output for the same
input, which is what makes the resulting traces directly comparable across
frameworks in the Console (Milestone 3 goal, see `Milestones.md`) and
usable as a golden-trace fixture corpus later (Milestone 7).

Import from an example app with:

    import sys, pathlib
    sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent / "shared"))
    import tools
"""

from __future__ import annotations

# Canonical fixture "documents" the search/read_page tools agree on. Each
# entry's `url` is the identifier `search_tool` hands back and that
# `read_page_tool` resolves — keeping the two tools' canned data in sync so
# a demo app can chain "search -> read the top hit" without special-casing
# any framework.
_DOCUMENTS: dict[str, dict[str, str]] = {
    "mcp-governance-gaps": {
        "keywords": ("model context protocol", "mcp governance", "mcp gaps", "mcp"),
        "title": "MCP Governance Gaps",
        "snippet": (
            "The Model Context Protocol spec intentionally leaves "
            "authentication, audit logging, per-tool guardrails, and cost "
            "accounting to implementers, creating a governance gap as MCP "
            "adoption outpaces the tooling that secures it."
        ),
        "body": (
            "Model Context Protocol (MCP) governance gaps, in depth:\n\n"
            "1. Authentication - the MCP spec does not mandate a single auth "
            "scheme, so most public MCP servers ship with no auth or "
            "ad-hoc API keys.\n"
            "2. Audit trails - tool-call history is rarely persisted "
            "outside the calling agent's own logs, making incident review "
            "difficult after the fact.\n"
            "3. Guardrails - there is no standard way to restrict which "
            "tools an agent may call, or with what arguments, at the "
            "protocol level.\n"
            "4. Cost accounting - MCP responses carry no standardized cost "
            "or usage metadata, so spend attribution is left to the caller.\n\n"
            "A governance layer that sits in front of any MCP server can "
            "close all four gaps without requiring changes to the server "
            "or the calling agent."
        ),
    },
    "agent-observability": {
        "keywords": ("agent observability", "observability", "tracing"),
        "title": "Agent Observability Fundamentals",
        "snippet": (
            "Agent observability means capturing the full DAG of an "
            "agent run - LLM calls, tool calls, and sub-agent handoffs - "
            "regardless of which orchestration framework produced it."
        ),
        "body": (
            "Agent observability fundamentals, in depth:\n\n"
            "Traditional APM captures request/response pairs. Agent "
            "observability must additionally capture branching, retries, "
            "and multi-step tool trajectories, since a single user request "
            "can fan out into dozens of LLM and tool calls across several "
            "cooperating agents. The minimum useful unit is a span with a "
            "kind (llm.call, tool.call, or agent.handoff), a parent/child "
            "link, and enough input/output to reconstruct what happened "
            "without re-running the agent."
        ),
    },
    "deterministic-replay": {
        "keywords": ("deterministic replay", "replay", "reproducibility"),
        "title": "Deterministic Replay for Agents",
        "snippet": (
            "Deterministic replay re-runs a historical agent trace exactly "
            "by substituting recorded or mocked tool responses for live "
            "ones, so non-determinism is confined to what was recorded."
        ),
        "body": (
            "Deterministic replay, in depth:\n\n"
            "Most agent non-determinism traces back to two sources: LLM "
            "sampling and tool I/O. Both can be recorded during a live run "
            "and substituted back in during replay, which is why example "
            "apps built for trace comparability should use canned tools "
            "and canned LLM responses from the start rather than adding "
            "determinism later."
        ),
    },
}

_GENERIC_TOPIC = "generic-topic"


def search_tool(query: str) -> str:
    """Return a deterministic, canned "search results" string for `query`.

    Matching is a simple case-insensitive substring/keyword check against a
    small fixed set of on-brand AgentMesh topics (see `_DOCUMENTS`); an
    unrecognized query falls back to a generic-but-stable result so any of
    the four example apps can safely reuse the same query string.
    """
    key = _resolve_topic(query)
    if key == _GENERIC_TOPIC:
        return (
            f'No indexed topic exactly matches "{query}". '
            "Showing the closest generic result:\n"
            "1. General Agent Engineering Notes "
            "(ref: generic-topic) - background material for topics not "
            "yet covered by the AgentMesh fixture knowledge base."
        )

    doc = _DOCUMENTS[key]
    return f"1. {doc['title']} (ref: {key})\n   {doc['snippet']}"


def read_page_tool(url_or_ref: str) -> str:
    """Return deterministic, canned "page content" for `url_or_ref`.

    `url_or_ref` is matched the same way `search_tool` selects a topic (and
    accepts the bare `ref: <key>` identifier `search_tool` emits), so
    reading the top hit from a `search_tool` call always resolves to
    consistent content.
    """
    key = _resolve_topic(url_or_ref)
    if key == _GENERIC_TOPIC:
        return (
            f'No cached page content for "{url_or_ref}". '
            "Generic fixture page: this is placeholder body text for "
            "topics outside the AgentMesh fixture knowledge base."
        )

    return _DOCUMENTS[key]["body"]


def _resolve_topic(text: str) -> str:
    """Resolve free-form text to a `_DOCUMENTS` key via keyword/substring
    matching, or `_GENERIC_TOPIC` if nothing matches. Deterministic:
    iterates `_DOCUMENTS` in insertion order and returns the first hit."""
    haystack = text.lower()
    for key, doc in _DOCUMENTS.items():
        if key in haystack:
            return key
        for keyword in doc["keywords"]:
            if keyword in haystack:
                return key
    return _GENERIC_TOPIC
