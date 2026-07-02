"""AutoGen reference example for AgentMesh Milestone 3.

Implements the shared workflow (`examples/shared/`) -- research topic ->
search tool -> read page tool -> LLM summary -> reviewer agent -> return
answer -- as a two-agent AutoGen `GroupChat` ("researcher" and "reviewer"),
instrumented with `agentmesh_autogen.AutoGenAdapter`.

No real LLM or network call is ever made: both agents get their content
from a `register_reply` reply function scripted to return the shared
fixtures' canned `FAKE_SUMMARY`/`FAKE_REVIEW` text, and the "tool calls"
run `examples/shared/tools.py`'s real deterministic functions through
AutoGen's real `execute_function` machinery (registered via
`function_map`). See `sdk/integrations/autogen/LIMITATIONS.md` for why
`ag2` (imported as `autogen`) is the package this targets, and
`agentmesh_autogen.adapter`'s module docstring for the framework-to-span
mapping this produces.
"""

from __future__ import annotations

import json
import pathlib
import re
import sys

from autogen import ConversableAgent, GroupChat, GroupChatManager

import agentmesh
from agentmesh_autogen import AutoGenAdapter

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent / "shared"))
import fixtures  # noqa: E402
import prompts  # noqa: E402
import tools  # noqa: E402

_REF_PATTERN = re.compile(r"\(ref: ([\w-]+)\)")


class InMemoryExporter:
    """Trivial in-memory stand-in for `agentmesh.exporter.BatchingExporter`
    so this demo runs standalone with no Collector and no network call, per
    the shared workflow's "no real API keys, no network calls, ever" rule.
    """

    def __init__(self) -> None:
        self.recorded: list = []

    def record(self, span) -> None:
        self.recorded.append(span)

    def shutdown(self) -> None:
        pass


def build_group_chat():
    """Wire the shared workflow's tools/prompts onto a two-agent AutoGen
    `GroupChat`. See `sdk/integrations/autogen/tests/test_autogen_adapter.py`
    for the same construction, asserted against directly."""
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
    return researcher, reviewer, manager


def main() -> None:
    tracer = agentmesh.Tracer(project_id="autogen-debate-demo", exporter=InMemoryExporter())
    adapter = AutoGenAdapter(tracer)

    researcher, reviewer, manager = build_group_chat()
    adapter.instrument(manager)

    # Phase 1: researcher's own research turn (search -> read_page ->
    # summarize), computed directly via the public `generate_reply()` API.
    researcher.generate_reply(messages=[{"role": "user", "content": prompts.RESEARCH_TOPIC}])
    # Phase 2: hand the summary into the group chat, which routes it to the
    # reviewer for the "review" step (an agent.handoff, per fixtures.py).
    researcher.initiate_chat(manager, message=prompts.FAKE_SUMMARY, clear_history=True, silent=True)

    spans = sorted(tracer._exporter.recorded, key=lambda s: s.start_time_ns)
    trace_id = spans[0].trace_id if spans else None

    print(f"Trace ID: {trace_id}")
    print(f"Expected logical steps: {fixtures.expected_step_names()}")
    print(f"Exported spans: {len(spans)}")
    for span in spans:
        indent = "  " if span.parent_span_id else ""
        output_preview = (span.output or "")[:80]
        print(f"{indent}- [{span.kind.value}] {span.name} -> {output_preview!r}")

    tracer.shutdown()


if __name__ == "__main__":
    main()
