import pytest
import agents
from agents import Runner, Agent, handoff, RunConfig
from agents.tracing import set_trace_processors
import agentmesh
from agentmesh_openai_agents import AgentMeshTracingProcessor, FakeModelProvider, ScriptedTurn

class FakeExporter:
    def __init__(self):
        self.recorded = []
    def record(self, span):
        self.recorded.append(span)
    def shutdown(self):
        pass

@pytest.fixture
def fake_tracer(monkeypatch):
    exporter = FakeExporter()
    tracer = agentmesh.Tracer(project_id="test-proj", exporter=exporter)
    monkeypatch.setattr("agentmesh.tracer._default_tracer", tracer)
    return tracer

@pytest.mark.asyncio
async def test_handoff_and_tool_call_emits_spans(fake_tracer):
    set_trace_processors([AgentMeshTracingProcessor(fake_tracer)])

    def search(query: str) -> str:
        return f"results for {query}"
    tool = agents.function_tool(search)

    triage_agent = Agent(name="triage", instructions="...")
    specialist = Agent(name="specialist", tools=[tool], instructions="...")

    triage_agent.handoffs = [handoff(specialist)]

    provider = FakeModelProvider([
        ScriptedTurn("start", "", [{"id":"call_1","type":"function","function":{"name":"transfer_to_specialist","arguments":"{}"}}]),
        ScriptedTurn('{"assistant": "specialist"}', "", [{"id":"call_2","type":"function","function":{"name":"search","arguments":"{\"query\":\"weather\"}"}}]),
        ScriptedTurn("results for weather", "All done")
    ])

    await Runner.run(triage_agent, "start", run_config=RunConfig(model_provider=provider))
    spans = fake_tracer._exporter.recorded
    kinds = [s.kind for s in spans]
    
    # We expect:
    # 1. LLM call (triage decides to handoff)
    # 2. Tool call (the handoff tool execution wrapper) - depending on how openai-agents structures the handoff
    # 3. Agent handoff span
    # 4. LLM call (specialist decides to search)
    # 5. Tool call (search)
    # 6. LLM call (specialist final answer)
    
    assert agentmesh.SpanKind.LLM_CALL in kinds
    assert agentmesh.SpanKind.TOOL_CALL in kinds
    assert agentmesh.SpanKind.AGENT_HANDOFF in kinds

    handoffs = [s for s in spans if s.kind == agentmesh.SpanKind.AGENT_HANDOFF]
    assert len(handoffs) >= 1
    assert "specialist" in handoffs[0].name
