import pytest

import agentmesh
from agentmesh._span import SpanKind, SpanStatus
from agentmesh.tracer import Tracer, _make_decorator


class FakeExporter:
    def __init__(self):
        self.recorded = []

    def record(self, span):
        self.recorded.append(span)

    def shutdown(self):
        pass


@pytest.fixture
def tracer_and_exporter():
    exporter = FakeExporter()
    tracer = Tracer(project_id="test-project", exporter=exporter)
    return tracer, exporter


def test_start_span_records_span_on_normal_exit(tracer_and_exporter):
    tracer, exporter = tracer_and_exporter
    with tracer.start_span(SpanKind.LLM_CALL, "gpt-4.1") as span:
        pass
    assert len(exporter.recorded) == 1
    assert exporter.recorded[0] is span
    assert span.status == SpanStatus.OK


def test_start_span_records_error_status_on_exception(tracer_and_exporter):
    tracer, exporter = tracer_and_exporter
    with pytest.raises(ValueError):
        with tracer.start_span(SpanKind.TOOL_CALL, "search") as span:
            raise ValueError("boom")
    assert len(exporter.recorded) == 1
    assert exporter.recorded[0].status == SpanStatus.ERROR


def test_nested_spans_share_trace_id_and_link_parent(tracer_and_exporter):
    tracer, exporter = tracer_and_exporter
    with tracer.start_span(SpanKind.AGENT_HANDOFF, "outer") as outer:
        with tracer.start_span(SpanKind.TOOL_CALL, "inner") as inner:
            pass
    assert inner.trace_id == outer.trace_id
    assert inner.parent_span_id == outer.span_id
    # both spans should have been recorded (inner first, since its `with`
    # block exits first)
    assert len(exporter.recorded) == 2
    assert exporter.recorded[0] is inner
    assert exporter.recorded[1] is outer


def test_sibling_spans_get_different_trace_ids(tracer_and_exporter):
    tracer, _ = tracer_and_exporter
    with tracer.start_span(SpanKind.LLM_CALL, "a") as span_a:
        pass
    with tracer.start_span(SpanKind.LLM_CALL, "b") as span_b:
        pass
    assert span_a.trace_id != span_b.trace_id
    assert span_a.parent_span_id is None
    assert span_b.parent_span_id is None


def test_decorator_wraps_function_and_records_span(monkeypatch, tracer_and_exporter):
    tracer, exporter = tracer_and_exporter
    monkeypatch.setattr("agentmesh.tracer._default_tracer", tracer)

    @agentmesh.trace_llm_call()
    def call_model(prompt: str) -> str:
        return "response to " + prompt

    result = call_model("hello")

    assert result == "response to hello"
    assert len(exporter.recorded) == 1
    span = exporter.recorded[0]
    assert span.kind == SpanKind.LLM_CALL
    assert span.name == "call_model"
    assert span.status == SpanStatus.OK
    assert "hello" in span.input
    assert "response to hello" in span.output


def test_decorator_uses_explicit_name_when_given(monkeypatch, tracer_and_exporter):
    tracer, exporter = tracer_and_exporter
    monkeypatch.setattr("agentmesh.tracer._default_tracer", tracer)

    @agentmesh.trace_tool_call(name="web_search")
    def search(query: str) -> str:
        return "results"

    search("weather today")
    assert exporter.recorded[0].name == "web_search"


def test_decorator_propagates_exception_and_marks_error(monkeypatch, tracer_and_exporter):
    tracer, exporter = tracer_and_exporter
    monkeypatch.setattr("agentmesh.tracer._default_tracer", tracer)

    @agentmesh.trace_tool_call()
    def failing_tool():
        raise RuntimeError("tool broke")

    with pytest.raises(RuntimeError):
        failing_tool()

    assert exporter.recorded[0].status == SpanStatus.ERROR


def test_decorator_raises_clear_error_without_configure():
    # No tracer configured (module-level _default_tracer is None by default
    # in a fresh interpreter state) — must raise a clear, actionable error,
    # not an obscure AttributeError deep in exporter code.
    import agentmesh.tracer as tracer_module

    original = tracer_module._default_tracer
    tracer_module._default_tracer = None
    try:
        @agentmesh.trace_llm_call()
        def call_model():
            return "x"

        with pytest.raises(RuntimeError, match="configure"):
            call_model()
    finally:
        tracer_module._default_tracer = original
