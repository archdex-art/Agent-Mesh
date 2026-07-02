import pytest

import agentmesh
from agentmesh._span import SpanKind, SpanStatus
from agentmesh.replay_shim import ReplayedCallError, reset_call_counters
from agentmesh.tracer import Tracer


class FakeExporter:
    def __init__(self):
        self.recorded = []

    def record(self, span):
        self.recorded.append(span)

    def shutdown(self):
        pass


@pytest.fixture
def tracer_and_exporter(monkeypatch):
    exporter = FakeExporter()
    tracer = Tracer(project_id="test-project", exporter=exporter)
    monkeypatch.setattr("agentmesh.tracer._default_tracer", tracer)
    reset_call_counters()
    yield tracer, exporter
    monkeypatch.delenv("AGENTMESH_REPLAY_ID", raising=False)
    reset_call_counters()


def _stub_lookup(monkeypatch, responses):
    """responses: list of (output, status_str) tuples returned in order,
    one per call_index."""
    calls = []

    def fake_fetch(kind, name, call_index):
        from agentmesh.replay_shim import RecordedCall

        calls.append((kind, name, call_index))
        output, status_str = responses[call_index]
        return RecordedCall(output=output, status=SpanStatus(status_str))

    monkeypatch.setattr("agentmesh.tracer.replay_shim.fetch_recorded_response", fake_fetch)
    return calls


def test_replay_mode_intercepts_call_and_never_executes_wrapped_function(tracer_and_exporter, monkeypatch):
    _, exporter = tracer_and_exporter
    monkeypatch.setenv("AGENTMESH_REPLAY_ID", "replay-abc")
    _stub_lookup(monkeypatch, [("recorded output", "ok")])

    call_count = 0

    @agentmesh.trace_tool_call(name="search")
    def search(query: str) -> str:
        nonlocal call_count
        call_count += 1
        return "REAL RESULT — should never appear"

    result = search("weather in sf")

    assert call_count == 0, "the real tool function must never execute during replay"
    assert result == "recorded output"
    span = exporter.recorded[0]
    assert span.status == SpanStatus.OK
    assert span.output == "recorded output"
    assert span.attributes["replay.replayed"] == "true"
    assert span.attributes["replay.call_index"] == "0"


def test_replay_mode_raises_replayed_call_error_for_recorded_failure(tracer_and_exporter, monkeypatch):
    _, exporter = tracer_and_exporter
    monkeypatch.setenv("AGENTMESH_REPLAY_ID", "replay-abc")
    _stub_lookup(monkeypatch, [("upstream 500", "error")])

    @agentmesh.trace_tool_call(name="search")
    def search(query: str) -> str:
        return "never reached"

    with pytest.raises(ReplayedCallError) as exc_info:
        search("weather in sf")

    assert exc_info.value.status == SpanStatus.ERROR
    span = exporter.recorded[0]
    assert span.status == SpanStatus.ERROR
    assert span.output == "upstream 500"


def test_replay_mode_call_index_increments_across_repeated_calls(tracer_and_exporter, monkeypatch):
    _, exporter = tracer_and_exporter
    monkeypatch.setenv("AGENTMESH_REPLAY_ID", "replay-abc")
    calls = _stub_lookup(monkeypatch, [("first", "ok"), ("second", "ok"), ("third", "ok")])

    @agentmesh.trace_tool_call(name="search")
    def search(query: str) -> str:
        return "unused"

    r1 = search("q1")
    r2 = search("q2")
    r3 = search("q3")

    assert [r1, r2, r3] == ["first", "second", "third"]
    assert calls == [("tool.call", "search", 0), ("tool.call", "search", 1), ("tool.call", "search", 2)]


def test_replay_mode_takes_priority_over_chaos(tracer_and_exporter, monkeypatch):
    # If both replay and chaos were somehow active, replay must win —
    # replaying historical data with an injected fault layered on top
    # would make the reconstructed trace diverge from what's being
    # replayed, defeating determinism.
    _, exporter = tracer_and_exporter
    monkeypatch.setenv("AGENTMESH_REPLAY_ID", "replay-abc")
    _stub_lookup(monkeypatch, [("recorded output", "ok")])

    from agentmesh.chaos import ErrorFault, configure_chaos

    configure_chaos(enabled=True, faults_by_tool={"search": [ErrorFault(message="boom", probability=1.0)]})
    try:
        call_count = 0

        @agentmesh.trace_tool_call(name="search")
        def search(query: str) -> str:
            nonlocal call_count
            call_count += 1
            return "real"

        result = search("q")
        assert result == "recorded output"
        assert call_count == 0
        span = exporter.recorded[0]
        assert "chaos.injected" not in span.attributes
        assert span.attributes["replay.replayed"] == "true"
    finally:
        configure_chaos(enabled=False)


def test_non_replay_mode_runs_normally_and_ignores_replay_env_var_when_unset(tracer_and_exporter):
    _, exporter = tracer_and_exporter

    @agentmesh.trace_tool_call(name="search")
    def search(query: str) -> str:
        return "live result"

    result = search("q")
    assert result == "live result"
    span = exporter.recorded[0]
    assert "replay.replayed" not in span.attributes


def test_replay_mode_works_for_llm_call_kind_too(tracer_and_exporter, monkeypatch):
    _, exporter = tracer_and_exporter
    monkeypatch.setenv("AGENTMESH_REPLAY_ID", "replay-abc")
    calls = _stub_lookup(monkeypatch, [("recorded completion", "ok")])

    @agentmesh.trace_llm_call(name="gpt-4.1")
    def call_model(prompt: str) -> str:
        return "real completion"

    result = call_model("hello")
    assert result == "recorded completion"
    assert calls == [("llm.call", "gpt-4.1", 0)]


def test_replay_mode_tags_every_span_with_source_replay_id(tracer_and_exporter, monkeypatch):
    _, exporter = tracer_and_exporter
    monkeypatch.setenv("AGENTMESH_REPLAY_ID", "replay-xyz-789")
    _stub_lookup(monkeypatch, [("recorded output", "ok")])

    @agentmesh.trace_tool_call(name="search")
    def search(query: str) -> str:
        return "unused"

    search("q")

    span = exporter.recorded[0]
    assert span.attributes["replay.replay_id"] == "replay-xyz-789"


def test_non_replay_mode_does_not_tag_replay_id(tracer_and_exporter):
    _, exporter = tracer_and_exporter

    @agentmesh.trace_tool_call(name="search")
    def search(query: str) -> str:
        return "live result"

    search("q")

    span = exporter.recorded[0]
    assert "replay.replay_id" not in span.attributes
