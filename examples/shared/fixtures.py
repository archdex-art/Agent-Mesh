"""The canonical, framework-agnostic step list for the Milestone 3 shared
workflow:

    Research topic -> Search tool -> Read page tool -> LLM summary
        -> Reviewer agent -> Return answer

Every one of the four example apps (LangGraph, CrewAI, AutoGen, OpenAI
Agents SDK) implements this same logical workflow so their traces are
directly comparable in the Console (`Milestones.md`, Milestone 3 success
criteria: "a human reviewer can look at any of the four traces and
identify the same logical steps without knowing which framework produced
it"). `expected_step_names()` is the reference other workstreams' example
apps, and any later golden-trace comparison test (`Milestones.md`,
Milestone 7's golden-trace regression suite), can assert against.

Deliberately NOT specified here: exact span *names* or framework-specific
IDs. Each framework's adapter names its spans however is idiomatic for
that framework (e.g. a LangGraph node name vs. a CrewAI task description);
only the *count*, *kind*, and *logical order* of steps are meant to be
comparable across frameworks, per `WORKFLOW_STEPS` below. Consumers should
assert on those, not on literal span names.

Import from an example app with:

    import sys, pathlib
    sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent / "shared"))
    import fixtures
"""

from __future__ import annotations

# `kind` mirrors the string values of agentmesh.SpanKind
# (sdk/python/agentmesh/_span.py) without importing the SDK, so this
# fixture module stays a dependency-free, plain-Python file. Every
# example app's corresponding step should emit exactly one span of the
# given kind, in this order (AGENT_HANDOFF for "review" because handing
# off to a reviewer agent is the one step of the four that is not a
# tool.call or llm.call, per Architecture.md's span-kind mapping table).
WORKFLOW_STEPS: list[dict[str, str]] = [
    {"name": "search", "kind": "tool.call"},
    {"name": "read_page", "kind": "tool.call"},
    {"name": "summarize", "kind": "llm.call"},
    {"name": "review", "kind": "agent.handoff"},
]


def expected_step_names() -> list[str]:
    """Return the canonical, ordered list of logical step names every
    framework's trace for the shared workflow SHOULD produce:
    `["search", "read_page", "summarize", "review"]`.

    This is a structural reference (count + order of logical steps), not a
    literal one: each framework will give its spans different concrete
    names. Use `WORKFLOW_STEPS` if you also need each step's expected
    canonical span kind.
    """
    return [step["name"] for step in WORKFLOW_STEPS]
