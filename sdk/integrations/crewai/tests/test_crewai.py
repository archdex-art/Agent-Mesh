import pytest
from crewai import Crew, Task, Agent, Process
from crewai.llm import LLM
import agentmesh
from agentmesh_crewai import instrument_crew

class FakeLLM(LLM):
    def call(self, messages, *args, **kwargs):
        return "fake completion"

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

def test_instrument_crew_emits_handoff_spans(fake_tracer):
    llm = FakeLLM(model="fake/model")
    a1 = Agent(role="A1", goal="g1", backstory="b1", llm=llm)
    a2 = Agent(role="A2", goal="g2", backstory="b2", llm=llm)
    
    t1 = Task(description="d1", expected_output="o1", agent=a1)
    t2 = Task(description="d2", expected_output="o2", agent=a2)
    
    crew = Crew(agents=[a1, a2], tasks=[t1, t2], process=Process.sequential)
    instrument_crew(crew, fake_tracer)
    
    with fake_tracer.start_span(agentmesh.SpanKind.AGENT_HANDOFF, "kickoff") as root:
        try:
            crew.kickoff()
        except Exception:
            pass
        root.finish(status=agentmesh.SpanStatus.OK, output="done")
    
    spans = fake_tracer._exporter.recorded
    handoffs = [s for s in spans if s.kind == agentmesh.SpanKind.AGENT_HANDOFF and s.name.startswith("handoff_from_")]
    
    assert len(handoffs) == 2
    assert "A1" in handoffs[0].name
    assert "A2" in handoffs[1].name
