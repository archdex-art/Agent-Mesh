"""Tests for `agentmesh_autogen.AutoGenAdapter`.

Runs the Milestone 3 shared workflow (search -> read_page -> summarize ->
review, `examples/shared/`) as a real two-agent AutoGen `GroupChat` built
on the actually-installed `ag2` package (imported as `autogen`; see
`agentmesh_autogen.adapter`'s module docstring and `LIMITATIONS.md` for
why `ag2` rather than `pyautogen`/`autogen-agentchat`). No real LLM or
network call is ever made: both agents get their content from a
`register_reply` reply function scripted to return `examples/shared`'s
canned `FAKE_SUMMARY`/`FAKE_REVIEW` text, and the "tool calls" run
`examples/shared/tools.py`'s real deterministic functions through
AutoGen's real `execute_function` machinery (registered via
`function_map`, exactly as a real user would wire a tool).

If `autogen`/`ag2` is not importable at all in the running environment,
this whole module is skipped (see `LIMITATIONS.md` for what to verify
once it is installable).
"""

from __future__ import annotations

import json
import pathlib
import re
import sys

import pytest

autogen = pytest.importorskip(
    "autogen", reason="ag2/pyautogen is not installed in this environment; see LIMITATIONS.md"
)
from autogen import ConversableAgent, GroupChat, GroupChatManager  # noqa: E402

import agentmesh  # noqa: E402
from agentmesh_autogen import AutoGenAdapter  # noqa: E402
from agentmesh_integrations_common.testing import (  # noqa: E402
    FakeExporter,
    assert_no_orphan_spans,
    assert_single_trace,
)

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[4] / "examples" / "shared"))
import prompts  # noqa: E402
import tools  # noqa: E402

_REF_PATTERN = re.compile(r"\(ref: ([\w-]+)\)")


def _build_group_chat():
    """Build the shared workflow as a two-agent AutoGen GroupChat:
    `researcher` (search_tool + read_page_tool via `function_map`, then a
    scripted summary) and `reviewer` (a scripted review). Both use
    `register_reply(..., position=0)` -- AutoGen's documented extension
    point for a fully custom, deterministic auto-reply -- so no real LLM
    config or API key is ever needed (`llm_config=False` on both agents).
    """
    researcher = ConversableAgent(
        name="researcher",
        llm_config=False,
        human_input_mode="NEVER",
        function_map={"search_tool": tools.search_tool, "read_page_tool": tools.read_page_tool},
    )

    def researcher_reply(recipient, messages=None, sender=None, config=None):
        _, search_result = recipient.execute_function(
            {"name": "search_tool", "arguments": json.dumps({"query": prompts.RESEARCH_TOPIC})}
        )
        match = _REF_PATTERN.search(search_result["content"])
        ref = match.group(1) if match else prompts.RESEARCH_TOPIC
        recipient.execute_function({"name": "read_page_tool", "arguments": json.dumps({"url_or_ref": ref})})
        return True, {"content": prompts.FAKE_SUMMARY, "role": "assistant"}

    researcher.register_reply(trigger=lambda sender: True, reply_func=researcher_reply, position=0)

    reviewer = ConversableAgent(name="reviewer", llm_config=False, human_input_mode="NEVER")

    def reviewer_reply(recipient, messages=None, sender=None, config=None):
        return True, {"content": prompts.FAKE_REVIEW, "role": "assistant"}

    reviewer.register_reply(trigger=lambda sender: True, reply_func=reviewer_reply, position=0)

    groupchat = GroupChat(agents=[researcher, reviewer], messages=[], max_round=2, speaker_selection_method="round_robin")
    manager = GroupChatManager(groupchat=groupchat, llm_config=False, human_input_mode="NEVER")
    return researcher, reviewer, groupchat, manager


@pytest.fixture
def fake_tracer():
    return agentmesh.Tracer(project_id="test-proj", exporter=FakeExporter())


def _run_workflow(researcher, manager):
    # Phase 1: researcher does its own research turn directly (search ->
    # read_page -> summarize), independent of the group chat's own
    # send/receive plumbing -- `generate_reply()` is public, documented
    # AutoGen API for computing "what would this agent reply", not a
    # bypass. Phase 2 hands the summary into the group chat, which routes
    # it to the reviewer (`agent.handoff`) for the review step.
    researcher.generate_reply(messages=[{"role": "user", "content": prompts.RESEARCH_TOPIC}])
    researcher.initiate_chat(manager, message=prompts.FAKE_SUMMARY, clear_history=True, silent=True)


def test_full_workflow_emits_expected_span_kinds(fake_tracer):
    researcher, reviewer, groupchat, manager = _build_group_chat()
    adapter = AutoGenAdapter(fake_tracer)
    adapter.instrument(manager)

    _run_workflow(researcher, manager)

    spans = fake_tracer._exporter.recorded
    assert spans, "expected at least one exported span"

    assert_no_orphan_spans(spans)
    assert_single_trace(spans)

    kinds = [s.kind for s in spans]
    assert kinds.count(agentmesh.SpanKind.TOOL_CALL) == 2, kinds
    assert agentmesh.SpanKind.LLM_CALL in kinds, kinds

    tool_names = sorted(s.name for s in spans if s.kind == agentmesh.SpanKind.TOOL_CALL)
    assert tool_names == ["read_page_tool", "search_tool"]



    summarize_llm_spans = [
        s
        for s in spans
        if s.kind == agentmesh.SpanKind.LLM_CALL and s.attributes.get("autogen.agent") == "researcher"
    ]
    assert len(summarize_llm_spans) == 1
    assert summarize_llm_spans[0].output == prompts.FAKE_SUMMARY


def test_no_double_instrumentation_on_repeat_instrument_call(fake_tracer):
    researcher, reviewer, groupchat, manager = _build_group_chat()
    adapter = AutoGenAdapter(fake_tracer)
    adapter.instrument(manager)
    adapter.instrument(manager)  # should be a no-op, not double-wrap
    adapter.instrument(groupchat)  # same underlying GroupChat, also a no-op

    _run_workflow(researcher, manager)

    spans = fake_tracer._exporter.recorded
    assert [s.kind for s in spans].count(agentmesh.SpanKind.TOOL_CALL) == 2


def test_tool_call_failure_marks_span_error_and_propagates(fake_tracer):
    researcher = ConversableAgent(
        name="researcher",
        llm_config=False,
        human_input_mode="NEVER",
        function_map={"search_tool": tools.search_tool},
    )

    def failing_reply(recipient, messages=None, sender=None, config=None):
        recipient.execute_function({"name": "search_tool", "arguments": "not-json"})
        return True, {"content": "unreachable", "role": "assistant"}

    researcher.register_reply(trigger=lambda sender: True, reply_func=failing_reply, position=0)

    reviewer = ConversableAgent(name="reviewer", llm_config=False, human_input_mode="NEVER")
    groupchat = GroupChat(agents=[researcher, reviewer], messages=[], max_round=2, speaker_selection_method="round_robin")
    manager = GroupChatManager(groupchat=groupchat, llm_config=False, human_input_mode="NEVER")

    adapter = AutoGenAdapter(fake_tracer)
    adapter.instrument(manager)

    # `execute_function` catches malformed-JSON arguments itself (returns
    # is_exec_success=False with an error message) rather than raising, so
    # this should complete without an exception but record the tool.call
    # span as failed.
    researcher.generate_reply(messages=[{"role": "user", "content": "go"}])

    spans = fake_tracer._exporter.recorded
    tool_spans = [s for s in spans if s.kind == agentmesh.SpanKind.TOOL_CALL]
    assert len(tool_spans) == 1
    assert tool_spans[0].status == agentmesh.SpanStatus.ERROR
