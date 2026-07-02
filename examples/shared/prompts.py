"""Shared prompt text and canned LLM/reviewer output for every Milestone 3
example app.

Every example app runs the same logical workflow:

    Research topic -> Search tool -> Read page tool -> LLM summary
        -> Reviewer agent -> Return answer

so its trace is comparable across frameworks in the Console. The example
apps use canned/fake LLMs (matching the `CannedLLM`/`FakeLLM` pattern in
`examples/crewai-research-crew/main.py` and
`sdk/integrations/crewai/tests/test_crewai.py`) rather than a real model, so
no API key is ever required to run or trace-compare a demo. `FAKE_SUMMARY`
and `FAKE_REVIEW` below are what those canned LLMs should return for the
prompts built here, keeping the four traces' `input`/`output` fields
comparable too.

Import from an example app with:

    import sys, pathlib
    sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent / "shared"))
    import prompts
"""

from __future__ import annotations

# On-brand AgentMesh research topic: MCP governance is the product's core
# wedge (Vision.md, Assumption A3; Feature Roadmap.md's MCP Gateway entry).
RESEARCH_TOPIC = "Model Context Protocol governance gaps"


def summarizer_prompt(research_notes: str) -> str:
    """Prompt instructing an LLM to summarize `research_notes` gathered by
    the search/read_page tools into a concise research summary."""
    return (
        "You are a research summarizer. Read the research notes below and "
        "produce a concise, factual summary (3-5 sentences) covering the "
        "key points. Do not introduce information that is not present in "
        "the notes.\n\n"
        "Research notes:\n"
        f"{research_notes}\n\n"
        "Summary:"
    )


def reviewer_prompt(summary: str) -> str:
    """Prompt instructing a reviewer agent to check `summary` for accuracy
    and completeness against the original research topic, and either
    approve it or request specific changes."""
    return (
        "You are a reviewer agent. Check the summary below for accuracy "
        "and completeness with respect to the research topic "
        f'"{RESEARCH_TOPIC}". Respond with either "APPROVED: <reason>" if '
        'the summary is accurate and complete, or "CHANGES REQUESTED: '
        '<specific changes>" if it is missing something or incorrect.\n\n'
        "Summary to review:\n"
        f"{summary}\n\n"
        "Review:"
    )


# Canned outputs standing in for a real LLM call, so every example app's
# `llm.call` (summarize) and `agent.handoff` (review) spans carry the same
# comparable output regardless of framework.
FAKE_SUMMARY = (
    "MCP adoption is outpacing MCP governance: the spec leaves "
    "authentication, audit trails, per-tool guardrails, and cost "
    "accounting to implementers, so most public MCP servers ship with "
    "little to no access control or usage tracking. Closing this gap "
    "requires a governance layer in front of MCP servers rather than "
    "waiting for the protocol itself to mandate one."
)

FAKE_REVIEW = (
    "APPROVED: the summary correctly identifies authentication, audit "
    "trails, guardrails, and cost accounting as the four unaddressed MCP "
    "governance gaps and accurately reflects the research notes with no "
    "unsupported claims."
)
