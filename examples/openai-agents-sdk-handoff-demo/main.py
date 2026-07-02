import asyncio
import os
import pathlib
import sys

# Add shared examples module to path
sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent / "shared"))
import fixtures
import prompts
import tools

import agentmesh
from agentmesh_openai_agents import AgentMeshTracingProcessor, FakeModelProvider, ScriptedTurn
from agents import Agent, Runner, function_tool, handoff
from agents.tracing import set_trace_processors

async def main():
    # 1. Configure the core tracer
    tracer = agentmesh.configure(
        project_id="fe375e79-b7aa-4bd5-a7e6-72ad6db48d5b",
        api_key="am_live_test",
        endpoint="localhost:4317"
    )

    # 2. Wire the adapter into the framework's hook system
    set_trace_processors([AgentMeshTracingProcessor(tracer)])

    # 3. Build the workflow
    search = function_tool(tools.search_tool)
    read_page = function_tool(tools.read_page_tool)

    researcher = Agent(
        name="researcher",
        instructions=prompts.summarizer_prompt("You are a researcher. Search and read, then summarize."),
        tools=[search, read_page]
    )
    reviewer = Agent(
        name="reviewer",
        instructions=prompts.reviewer_prompt("You are a reviewer."),
    )

    # Add handoff
    researcher.handoffs = [handoff(reviewer)]


    # 4. Script the Fake LLM (since we aren't using a real API key)
    provider = FakeModelProvider([
        ScriptedTurn(
            "start", "", 
            [{"id": "call_1", "type": "function", "function": {"name": "search_tool", "arguments": '{"query": "Model Context Protocol governance gaps"}'}}]
        ),
        ScriptedTurn(
            "mcp-governance-gaps", "",
            [{"id": "call_2", "type": "function", "function": {"name": "read_page_tool", "arguments": '{"url_or_ref": "mcp-governance-gaps"}'}}]
        ),
        ScriptedTurn(
            "governance gaps, in depth", prompts.FAKE_SUMMARY,
            [{"id": "call_3", "type": "function", "function": {"name": "transfer_to_reviewer", "arguments": "{}"}}]
        ),
        ScriptedTurn('{"assistant": "reviewer"}', prompts.FAKE_REVIEW)
    ])

    # 5. Run it
    print("=== Milestone 3 shared workflow (OpenAI Agents SDK) ===")
    
    result = await Runner.run(researcher, "start", run_config=__import__("agents").RunConfig(model_provider=provider))
    
    print("\nFinal answer:", result.final_output)

    # Note: tracer.shutdown() blocks to flush the span queue to the exporter
    tracer.shutdown()
    
    # Normally we wouldn't reach into the private exporter, but since this
    # is a disconnected sandbox demo, we print the captured spans directly
    spans = tracer._exporter._batch if hasattr(tracer._exporter, "_batch") else []
    if not spans and hasattr(tracer._exporter, "recorded"):
        spans = tracer._exporter.recorded
        
    if not spans:
        print("\nNote: spans were flushed to the Collector (if running), or dropped if unreachable.")
    else:
        print(f"\nTrace ID: {spans[0].trace_id}")
        print(f"Emitted {len(spans)} workflow span(s):")
        for i, s in enumerate(spans, 1):
            print(f"  {i}. [{s.kind.value:<13}] {s.name:<12} status={s.status.value if s.status else 'ok'}")

if __name__ == "__main__":
    asyncio.run(main())
