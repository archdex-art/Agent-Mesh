import time

import pytest

import agentmesh
from agentmesh._span import SpanKind, SpanStatus
from agentmesh.chaos import ChaosInjectedError, ErrorFault, LatencyFault, configure_chaos
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
    yield tracer, exporter
    # Reset chaos policy after each test so faults don't leak between tests.
    configure_chaos(enabled=False)


def test_error_fault_prevents_wrapped_function_from_running(tracer_and_exporter):
    _, exporter = tracer_and_exporter
    configure_chaos(enabled=True, faults_by_tool={"flaky_search": [ErrorFault(probability=1.0)]}, seed=1)

    call_count = 0

    @agentmesh.trace_tool_call(name="flaky_search")
    def search(query: str) -> str:
        nonlocal call_count
        call_count += 1
        return "real result"

    with pytest.raises(ChaosInjectedError):
        search("weather")

    assert call_count == 0, "wrapped function must not execute when an ErrorFault fires"


def test_error_fault_marks_span_as_error_with_chaos_attributes(tracer_and_exporter):
    _, exporter = tracer_and_exporter
    configure_chaos(enabled=True, faults_by_tool={"flaky_search": [ErrorFault(probability=1.0)]}, seed=1)

    @agentmesh.trace_tool_call(name="flaky_search")
    def search(query: str) -> str:
        return "real result"

    with pytest.raises(ChaosInjectedError):
        search("weather")

    assert len(exporter.recorded) == 1
    span = exporter.recorded[0]
    assert span.status == SpanStatus.ERROR
    assert span.attributes["chaos.injected"] == "true"
    assert span.attributes["chaos.fault_type"] == "error"


def test_latency_fault_delays_but_still_executes_wrapped_function(tracer_and_exporter, monkeypatch):
    _, exporter = tracer_and_exporter
    slept = []
    monkeypatch.setattr("agentmesh.chaos.time.sleep", lambda s: slept.append(s))

    configure_chaos(enabled=True, faults_by_tool={"slow_search": [LatencyFault(seconds=3.0, probability=1.0)]}, seed=1)

    @agentmesh.trace_tool_call(name="slow_search")
    def search(query: str) -> str:
        return "delayed result"

    result = search("weather")

    assert result == "delayed result"
    assert slept == [3.0]
    assert len(exporter.recorded) == 1
    span = exporter.recorded[0]
    assert span.status == SpanStatus.OK
    assert span.attributes["chaos.injected"] == "true"
    assert span.attributes["chaos.fault_type"] == "latency"


def test_no_fault_configured_runs_normally(tracer_and_exporter):
    _, exporter = tracer_and_exporter
    configure_chaos(enabled=True, faults_by_tool={})  # enabled, but nothing targets this tool

    @agentmesh.trace_tool_call(name="untouched_tool")
    def search(query: str) -> str:
        return "clean result"

    result = search("weather")
    assert result == "clean result"
    span = exporter.recorded[0]
    assert "chaos.injected" not in span.attributes


def test_chaos_disabled_never_intercepts_even_with_faults_configured(tracer_and_exporter):
    _, exporter = tracer_and_exporter
    configure_chaos(enabled=False, faults_by_tool={"search": [ErrorFault(probability=1.0)]})

    @agentmesh.trace_tool_call(name="search")
    def search(query: str) -> str:
        return "clean result"

    result = search("weather")
    assert result == "clean result"
    span = exporter.recorded[0]
    assert span.status == SpanStatus.OK
    assert "chaos.injected" not in span.attributes


def test_real_exception_from_wrapped_function_is_not_mislabeled_as_chaos(tracer_and_exporter):
    # A genuine bug in the wrapped function (chaos disabled) must be
    # recorded as a normal error, not tagged with chaos.* attributes —
    # otherwise a real production failure would look like a deliberate
    # test injection in the trace.
    _, exporter = tracer_and_exporter
    configure_chaos(enabled=False)

    @agentmesh.trace_tool_call(name="buggy_tool")
    def buggy() -> str:
        raise RuntimeError("a real bug")

    with pytest.raises(RuntimeError):
        buggy()

    span = exporter.recorded[0]
    assert span.status == SpanStatus.ERROR
    assert "chaos.injected" not in span.attributes
